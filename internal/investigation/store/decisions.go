package store

import (
	"fmt"
	"reflect"
	"time"

	inv "github.com/yunus/safina/internal/investigation"
)

func pending(current *inv.Investigation, id string, revision int) (*inv.Proposal, error) {
	for i := range current.Proposals {
		p := &current.Proposals[i]
		if p.ID == id && p.Revision == revision {
			if p.State != "pending" {
				return nil, inv.ErrConflict
			}
			return p, nil
		}
	}
	return nil, inv.ErrNotFound
}

// Revise is an operator-only domain transition. It is not registered on the
// investigation transport. M2 will prepare/validate supported edits before use.
func (s *Store) Revise(id, proposal string, revision int, operator string, alternative inv.Alternative) (inv.Proposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.investigations[id]
	if !ok {
		return inv.Proposal{}, inv.ErrNotFound
	}
	current := clone(original)
	p, err := pending(&current, proposal, revision)
	if err != nil {
		return inv.Proposal{}, err
	}
	if operator == "" {
		return inv.Proposal{}, inv.ErrInvalid
	}
	if s.redact(alternative.Action.Image) != alternative.Action.Image || s.redact(alternative.Action.Container) != alternative.Action.Container {
		return inv.Proposal{}, inv.ErrInvalid
	}
	if !evidenceValid(current, alternative.Evidence) {
		return inv.Proposal{}, inv.ErrInvalid
	}
	if err = alternative.Validate(current.Cluster); err != nil {
		return inv.Proposal{}, err
	}
	if alternative.Target != p.Alternative.Target || alternative.Action.Kind != p.Alternative.Action.Kind {
		return inv.Proposal{}, inv.ErrConflict
	}
	alternative.Rationale = s.redact(alternative.Rationale)
	alternative.Verification = s.redact(alternative.Verification)
	p.State = "superseded"
	next := inv.Proposal{ID: proposal, Revision: revision + 1, Group: p.Group, Author: "operator:" + operator, State: "pending", Alternative: alternative}
	current.Proposals = append(current.Proposals, next)
	if err = s.commit("revised", &current, nil, ""); err != nil {
		return inv.Proposal{}, err
	}
	// Detach return values (Action contains a pointer).
	return clone(current).Proposals[len(current.Proposals)-1], nil
}

// Select atomically persists selection, sibling invalidation, decision, intent
// and the sole execution claim. The bool grants a send only to the first caller
// after a successful sync. Repeated selections and replay NEVER grant a send.
// This internal primitive is not an approval interface: M2 must validate a
// broker-prepared review and explicit operator intent before calling it.
func (s *Store) Select(id, proposal string, revision int, review, digest, operator string, overrides []string) (inv.Execution, bool, error) {
	return s.selectPrepared(id, proposal, revision, review, digest, operator, overrides, nil)
}

func (s *Store) SelectReviewed(r inv.Review, operator string, overrides []string) (inv.Execution, bool, error) {
	return s.selectPrepared(r.Investigation, r.Proposal, r.Revision, r.ID, r.Digest, operator, overrides, &r)
}

func (s *Store) selectPrepared(id, proposal string, revision int, review, digest, operator string, overrides []string, prepared *inv.Review) (inv.Execution, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.investigations[id]
	if !ok {
		return inv.Execution{}, false, inv.ErrNotFound
	}
	if review == "" || digest == "" || operator == "" {
		return inv.Execution{}, false, inv.ErrInvalid
	}
	current := clone(original)
	for _, d := range current.Decisions {
		if d.Choice == "select" && d.Proposal == proposal && d.Revision == revision {
			if d.Review != review || d.Digest != digest {
				return inv.Execution{}, false, inv.ErrConflict
			}
			for _, op := range current.Executions {
				if op.ID == d.Operation {
					return op, false, nil
				}
			}
		}
	}
	p, err := pending(&current, proposal, revision)
	if err != nil {
		return inv.Execution{}, false, err
	}
	if prepared != nil && (prepared.Target != p.Alternative.Target || !reflect.DeepEqual(prepared.Action, p.Alternative.Action) || prepared.ResourceVersion == "" || prepared.Noop) {
		return inv.Execution{}, false, inv.ErrConflict
	}
	group := p.Group
	p.State = "selected"
	for i := range current.Proposals {
		sibling := &current.Proposals[i]
		if sibling.Group == group && sibling.State == "pending" {
			sibling.State = "unavailable"
		}
	}
	now := time.Now().UTC()
	d := inv.Decision{Prepared: prepared, ID: inv.ID(), Proposal: proposal, Revision: revision, Review: review, Digest: digest, Operator: operator, Time: now, Choice: "select", Overrides: overrides, Operation: inv.ID()}
	op := inv.Execution{ID: d.Operation, Claim: inv.ID(), Decision: d.ID, State: "intent", Time: now}
	current.Decisions = append(current.Decisions, d)
	current.Executions = append(current.Executions, op)
	if err = s.commit("selected", &current, nil, op.ID); err != nil {
		return inv.Execution{}, false, err
	}
	return op, true, nil
}

func (s *Store) Reject(id, proposal string, revision int, operator string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.investigations[id]
	if !ok {
		return inv.ErrNotFound
	}
	if operator == "" {
		return inv.ErrInvalid
	}
	current := clone(original)
	p, err := pending(&current, proposal, revision)
	if err != nil {
		return err
	}
	p.State = "rejected"
	current.Decisions = append(current.Decisions, inv.Decision{ID: inv.ID(), Proposal: proposal, Revision: revision, Operator: operator, Time: time.Now().UTC(), Choice: "reject"})
	return s.commit("rejected", &current, nil, "")
}

// RecordOutcome only advances the existing operation; it never creates a claim.
func (s *Store) RecordOutcome(id string, outcome inv.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.investigations[id]
	if !ok {
		return inv.ErrNotFound
	}
	current := clone(original)
	for i, op := range current.Executions {
		if op.ID != outcome.ID {
			continue
		}
		if !outcomeAllowed(op, outcome) {
			return inv.ErrConflict
		}
		if outcome.State == "accepted" && (outcome.UID == "" || outcome.ResourceVersion == "") {
			return inv.ErrInvalid
		}
		outcome.Detail = s.redact(outcome.Detail)
		outcome.Claim = op.Claim
		outcome.Decision = op.Decision
		outcome.Time = time.Now().UTC()
		current.Executions[i] = outcome
		return s.commit("outcome", &current, nil, outcome.ID)
	}
	return fmt.Errorf("%w: operation", inv.ErrNotFound)
}

func outcomeAllowed(before, after inv.Execution) bool {
	switch before.State {
	case "intent":
		return after.State == "accepted" || after.State == "failed" || after.State == "unknown"
	case "accepted":
		return (after.State == "accepted" && after.Detail != before.Detail) || after.State == "completed" || after.State == "failed" || after.State == "superseded" || after.State == "unknown"
	case "unknown":
		return after.State == "superseded" || (after.State == "unknown" && after.Detail != before.Detail)
	default:
		return false
	}
}
