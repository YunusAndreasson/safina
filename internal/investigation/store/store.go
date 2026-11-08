// Package store owns the single-writer, durable investigation journal.
package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/YunusAndreasson/safina/internal/localstate"
	"golang.org/x/sys/unix"
)

const MaxRecordBytes = 8 << 20

type receipt struct {
	Key           string `json:"key"`
	Digest        string `json:"digest"`
	Investigation string `json:"investigation"`
	Result        string `json:"result"`
}

type event struct {
	Version  int       `json:"version"`
	ID       string    `json:"eventId"`
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
	Type     string    `json:"type"`
	Payload  payload   `json:"payload"`
}

type executionUpdate struct {
	Investigation string        `json:"investigation"`
	Execution     inv.Execution `json:"execution"`
}

type payload struct {
	ExecutionUpdate *executionUpdate   `json:"executionUpdate,omitempty"`
	Investigation   *inv.Investigation `json:"investigation,omitempty"`
	Receipt         *receipt           `json:"receipt,omitempty"`
	Outbox          *history.Record    `json:"outbox,omitempty"`
	Delivered       string             `json:"delivered,omitempty"`
}

type Store struct {
	mu             sync.Mutex
	deliveryMu     sync.Mutex
	outboxSequence map[string]uint64
	file           *os.File
	lock           *os.File
	sequence       uint64
	investigations map[string]inv.Investigation
	receipts       map[string]receipt
	outbox         map[string]history.Record
	warnings       []string
	failed         error
	closed         bool
	// Kept injectable in package tests to exercise uncertain disk failures.
	syncFile func() error
	redact   func(string) string
}

func Open(dir string) (*Store, error) {
	return OpenRedacted(dir, (*history.Store)(nil).Redact)
}

// OpenRedacted applies configured redaction after hashing requests for deduplication
// and before persisting human text. The raw request is never journaled.
func OpenRedacted(dir string, redact func(string) string) (*Store, error) {
	if redact == nil {
		redact = (*history.Store)(nil).Redact
	}
	if err := localstate.Directory(dir); err != nil {
		return nil, err
	}
	lock, err := localstate.Lock(filepath.Join(dir, "writer.lock"))
	if err != nil {
		return nil, err
	}
	s := &Store{redact: redact, lock: lock, investigations: map[string]inv.Investigation{}, receipts: map[string]receipt{}, outbox: map[string]history.Record{}, outboxSequence: map[string]uint64{}}
	s.file, err = localstate.Open(filepath.Join(dir, "journal.jsonl"), unix.O_CREAT|unix.O_RDWR|unix.O_APPEND)
	if err == nil {
		err = localstate.SyncDirectory(dir)
	}
	if err == nil {
		s.syncFile = s.file.Sync
		err = s.recover(dir)
	}
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	tails, err := filepath.Glob(filepath.Join(dir, "interrupted-*.tail"))
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	if len(tails) > 0 && len(s.warnings) == 0 {
		s.warnings = append(s.warnings, "Previously interrupted journal tails retained; coverage may be incomplete")
	}
	// Any request intent recovered from disk may already have reached the API.
	// Persist uncertainty without producing a fresh execution claim.
	for _, original := range s.investigations {
		current := clone(original)
		for i := range current.Executions {
			if current.Executions[i].State != "intent" {
				continue
			}
			current.Executions[i].State = "unknown"
			current.Executions[i].Detail = "broker interrupted; reconcile through reads, never replay"
			if err = s.commit("interrupted", &current, nil, current.Executions[i].ID); err != nil {
				_ = s.Close()
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) recover(dir string) error {
	r := bufio.NewReader(s.file)
	var offset int64
	for {
		var line []byte
		for {
			part, err := r.ReadSlice('\n')
			line = append(line, part...)
			if len(line) > MaxRecordBytes {
				return errors.New("journal record exceeds limit")
			}
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			if errors.Is(err, io.EOF) {
				if len(line) == 0 {
					return nil
				}
				// Preserve the incomplete tail durably before removing it from the journal.
				path := filepath.Join(dir, "interrupted-"+inv.ID()+".tail")
				f, openErr := localstate.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
				if openErr != nil {
					return openErr
				}
				_, writeErr := f.Write(line)
				if writeErr == nil {
					writeErr = f.Sync()
				}
				closeErr := f.Close()
				if err = errors.Join(writeErr, closeErr, localstate.SyncDirectory(dir)); err != nil {
					return err
				}
				if err = s.file.Truncate(offset); err != nil {
					return err
				}
				if err = s.file.Sync(); err != nil {
					return err
				}
				s.warnings = append(s.warnings, "Truncated journal tail preserved; last transition may not have completed")
				return nil
			}
			if err != nil {
				return err
			}
			break
		}
		var e event
		if err := strict(line, &e); err != nil {
			return fmt.Errorf("corrupt journal at %d: %w", offset, err)
		}
		if e.Version != inv.Version || e.Sequence != s.sequence+1 || e.ID == "" {
			return errors.New("unsupported or out-of-order journal record")
		}
		switch e.Type {
		case "evidence", "started", "submitted", "revised", "selected", "rejected", "outcome", "interrupted", "delivered":
		default:
			return errors.New("unsupported journal transition")
		}
		if e.Type != "delivered" && e.Payload.Investigation == nil && e.Payload.ExecutionUpdate == nil {
			return errors.New("invalid journal transition")
		}
		if err := s.validateExecutionUpdate(e); err != nil {
			return err
		}
		s.apply(e)
		offset += int64(len(line))
	}
}

func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}

func (s *Store) apply(e event) {
	s.sequence = e.Sequence
	p := e.Payload
	if update := p.ExecutionUpdate; update != nil {
		current := s.investigations[update.Investigation]
		for i := range current.Executions {
			if current.Executions[i].ID == update.Execution.ID {
				current.Executions[i] = update.Execution
				break
			}
		}
		s.investigations[current.ID] = current
	}
	if p.Investigation != nil {
		s.investigations[p.Investigation.ID] = *p.Investigation
	}
	if p.Receipt != nil {
		s.receipts[p.Receipt.Key] = *p.Receipt
	}
	if p.Outbox != nil {
		s.outbox[p.Outbox.ID] = *p.Outbox
		s.outboxSequence[p.Outbox.ID] = e.Sequence
	}
	if p.Delivered != "" {
		delete(s.outbox, p.Delivered)
		delete(s.outboxSequence, p.Delivered)
	}
}

func (s *Store) appendEvent(e event) error {
	if s.closed || s.failed != nil {
		return fmt.Errorf("%w: writer closed or failed", inv.ErrRecording)
	}
	if err := s.validateExecutionUpdate(e); err != nil {
		return err
	}
	// A journal event must also be deliverable to the smaller logbook format.
	// Otherwise one permanent outbox failure blocks every later approval.
	if e.Payload.Outbox != nil {
		outbox := *e.Payload.Outbox
		outbox.Detail = s.redact(outbox.Detail)
		outbox.Parameters = make(map[string]string, len(e.Payload.Outbox.Parameters))
		for key, value := range e.Payload.Outbox.Parameters {
			outbox.Parameters[key] = s.redact(value)
		}
		encoded, err := json.Marshal(outbox)
		if err != nil {
			return err
		}
		if len(encoded) > history.LineBytes {
			return fmt.Errorf("%w: logbook event limit reached", inv.ErrInvalid)
		}
	}
	e.Version = inv.Version
	e.Sequence = s.sequence + 1
	e.ID = inv.ID()
	e.Time = time.Now().UTC()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxRecordBytes {
		return fmt.Errorf("%w: investigation journal record limit reached", inv.ErrInvalid)
	}
	b = append(b, '\n')
	n, err := s.file.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.syncFile()
	}
	if err != nil {
		s.failed = err
		return fmt.Errorf("%w: %w", inv.ErrRecording, err)
	}
	// Apply only after sync; a failed/uncertain append poisons this writer.
	var detached event
	if err = json.Unmarshal(b, &detached); err != nil {
		return err
	}
	s.apply(detached)
	return nil
}

func (s *Store) commit(kind string, current *inv.Investigation, rc *receipt, operation string) error {
	target := history.Target{Cluster: current.Cluster, Context: current.Context}
	if current.Target != nil {
		target.Namespace = current.Target.Namespace
		target.Kind = current.Target.Kind
		target.Name = current.Target.Name
		target.UID = current.Target.UID
	}
	record := &history.Record{Version: 1, ID: inv.ID(), Session: current.Session, Time: time.Now().UTC(), Target: target, Action: "investigation_" + kind, Outcome: "recorded", Investigation: current.ID}
	record.Parameters = map[string]string{"transition": kind}
	if kind == "submitted" || kind == "revised" {
		latest := current.Proposals[len(current.Proposals)-1]
		record.Proposal = latest.ID
		record.Revision = latest.Revision
		record.Parameters["group"] = latest.Group
		record.Outcome = "pending"
	}
	if kind == "outcome" || kind == "interrupted" {
		record.Outcome = kind
		for _, op := range current.Executions {
			if op.ID == operation {
				record.Parameters["operation."+op.ID] = op.State
				break
			}
		}
	}
	record.Parameters["proposalCount"] = strconv.Itoa(len(current.Proposals))
	if len(current.Decisions) > 0 && (kind == "selected" || kind == "rejected" || kind == "outcome" || kind == "interrupted") {
		decision := current.Decisions[len(current.Decisions)-1]
		if operation != "" {
			for _, candidate := range current.Decisions {
				if candidate.Operation == operation {
					decision = candidate
					break
				}
			}
		}
		record.Decision = decision.ID
		record.Proposal = decision.Proposal
		record.Revision = decision.Revision
		record.Operation = decision.Operation
		record.Parameters["operator"] = decision.Operator
		record.Parameters["overrides"] = strings.Join(decision.Overrides, ",")
		if decision.Prepared != nil {
			r := decision.Prepared
			record.Parameters["review"] = r.ID
			record.Parameters["digest"] = r.Digest
			record.Parameters["replicas.before"] = strconv.FormatInt(int64(r.Before), 10)
			record.Parameters["replicas.after"] = strconv.FormatInt(int64(r.After), 10)
			record.Parameters["resourceVersion"] = r.ResourceVersion
			record.Parameters["image"] = r.Action.Image
			record.Parameters["container"] = r.Action.Container
			record.Parameters["rollbackRevision"] = strconv.FormatInt(r.Action.Revision, 10)
			record.Parameters["restartAt"] = r.RestartAt
		}
	}
	if operation != "" {
		record.Operation = operation
		for _, op := range current.Executions {
			if op.ID == operation {
				record.Outcome = op.State
				record.Detail = s.redact(op.Detail)
				break
			}
		}
	}
	for _, p := range current.Proposals {
		if p.ID == record.Proposal && p.Revision == record.Revision {
			t := p.Alternative.Target
			record.Target = history.Target{Cluster: t.Cluster, Context: current.Context, Namespace: t.Namespace, Kind: t.Kind, Name: t.Name, UID: t.UID}
			record.Parameters["action"] = p.Alternative.Action.Kind
			break
		}
	}
	p := payload{Investigation: current, Receipt: rc, Outbox: record}
	if kind == "outcome" || kind == "interrupted" {
		for _, op := range current.Executions {
			if op.ID == operation {
				p.Investigation = nil
				p.ExecutionUpdate = &executionUpdate{Investigation: current.ID, Execution: op}
				break
			}
		}
	}
	return s.appendEvent(event{Type: kind, Payload: p})
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.file != nil {
		err = s.file.Close()
	}
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
	}
	return err
}

// clone detaches mutable state without a JSON round trip, which can fail on
// timestamps and silently erase the investigation.
func clone(v inv.Investigation) inv.Investigation {
	if v.Target != nil {
		target := *v.Target
		v.Target = &target
	}
	v.Evidence = slices.Clone(v.Evidence)
	for i := range v.Evidence {
		v.Evidence[i].Warnings = slices.Clone(v.Evidence[i].Warnings)
	}
	v.Proposals = slices.Clone(v.Proposals)
	for i := range v.Proposals {
		a := &v.Proposals[i].Alternative
		a.Evidence = slices.Clone(a.Evidence)
		a.Action = cloneAction(a.Action)
	}
	v.Decisions = slices.Clone(v.Decisions)
	for i := range v.Decisions {
		d := &v.Decisions[i]
		d.Overrides = slices.Clone(d.Overrides)
		if d.Prepared != nil {
			r := *d.Prepared
			r.Action = cloneAction(r.Action)
			// Executable patch bytes are transient and were also omitted by JSON cloning.
			r.Patch = nil
			r.Warnings = slices.Clone(r.Warnings)
			r.RequiredOverrides = slices.Clone(r.RequiredOverrides)
			d.Prepared = &r
		}
	}
	v.Executions = slices.Clone(v.Executions)
	return v
}

func cloneAction(a inv.Action) inv.Action {
	if a.Replicas != nil {
		replicas := *a.Replicas
		a.Replicas = &replicas
	}
	return a
}

func (s *Store) Get(id string) (inv.Investigation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.investigations[id]
	if !ok {
		return inv.Investigation{}, inv.ErrNotFound
	}
	return clone(current), nil
}

func (s *Store) Health() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	warnings := append([]string(nil), s.warnings...)
	if s.failed != nil {
		warnings = append(warnings, "Investigation writer failed; restart required")
	}
	return warnings, len(s.outbox)
}

// Deliver retries only logbook writes. The stable record survives an uncertain
// delivery acknowledgement and is deduplicated by the destination.
func (s *Store) Deliver(appendRecord func(history.Record) error) error {
	// Serialize deliveries, but never hold the state lock across logbook I/O.
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	s.mu.Lock()
	pending := make([]history.Record, 0, len(s.outbox))
	for _, record := range s.outbox {
		record.Parameters = maps.Clone(record.Parameters)
		if record.SourceTime != nil {
			record.SourceTime = new(*record.SourceTime)
		}
		pending = append(pending, record)
	}
	sort.Slice(pending, func(i, j int) bool {
		return s.outboxSequence[pending[i].ID] < s.outboxSequence[pending[j].ID]
	})
	s.mu.Unlock()
	for _, record := range pending {
		if err := appendRecord(record); err != nil {
			return err
		}
		s.mu.Lock()
		err := s.appendEvent(event{Type: "delivered", Payload: payload{Delivered: record.ID}})
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// Compact outcomes reference an existing operation and cannot introduce a
// claim or replace the investigation's proposals/evidence. Legacy full snapshots
// remain readable. Validate references before applying either disk or live data.
func (s *Store) validateExecutionUpdate(e event) error {
	u := e.Payload.ExecutionUpdate
	if u == nil {
		return nil
	}
	if (e.Type != "outcome" && e.Type != "interrupted") || e.Payload.Investigation != nil || e.Payload.Receipt != nil {
		return errors.New("invalid compact execution transition")
	}
	if u.Execution.State == "accepted" && (u.Execution.UID == "" || u.Execution.ResourceVersion == "") {
		return errors.New("compact accepted execution has no API provenance")
	}
	current, ok := s.investigations[u.Investigation]
	if ok {
		for _, previous := range current.Executions {
			if previous.ID == u.Execution.ID && previous.Decision == u.Execution.Decision && previous.Claim == u.Execution.Claim && outcomeAllowed(previous, u.Execution) {
				return nil
			}
		}
	}
	return errors.New("compact execution transition has no valid predecessor")
}

func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) retry(session, method, key string, request any) (receipt, bool, error) {
	if session == "" || key == "" || len(session) > 256 || len(key) > 256 {
		return receipt{}, false, fmt.Errorf("%w: session and idempotency key required (maximum 256 bytes)", inv.ErrInvalid)
	}
	receiptKey, err := digest([]string{session, method, key})
	if err != nil {
		return receipt{}, false, err
	}
	requestDigest, err := digest(request)
	if err != nil {
		return receipt{}, false, err
	}
	rc := receipt{Key: receiptKey, Digest: requestDigest}
	prior, ok := s.receipts[rc.Key]
	if ok && prior.Digest != rc.Digest {
		return rc, false, inv.ErrConflict
	}
	if ok {
		return prior, true, nil
	}
	return rc, false, nil
}

func (s *Store) Start(current inv.Investigation, key string) (inv.Investigation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current.Cluster == "" || current.Context == "" {
		return inv.Investigation{}, fmt.Errorf("%w: explicit cluster required", inv.ErrInvalid)
	}
	if current.Target != nil {
		if err := current.Target.Validate(current.Cluster); err != nil {
			return inv.Investigation{}, err
		}
	}
	if current.ID != "" || len(current.Proposals)+len(current.Decisions)+len(current.Executions)+len(current.Evidence) != 0 {
		return inv.Investigation{}, inv.ErrInvalid
	}
	rc, found, err := s.retry(current.Session, "start", key, current)
	if err != nil {
		return inv.Investigation{}, err
	}
	if found {
		return clone(s.investigations[rc.Investigation]), nil
	}
	current.Summary = s.redact(current.Summary)
	current.ID = inv.ID()
	current.Created = time.Now().UTC()
	rc.Investigation = current.ID
	rc.Result = current.ID
	if err = s.commit("started", &current, &rc, ""); err != nil {
		return inv.Investigation{}, err
	}
	return clone(current), nil
}

func (s *Store) Submit(id, session, key string, alternatives []inv.Alternative) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.investigations[id]
	if !ok {
		return "", inv.ErrNotFound
	}
	if len(alternatives) == 0 || len(alternatives) > 16 {
		return "", fmt.Errorf("%w: submit 1 to 16 alternatives", inv.ErrInvalid)
	}
	for _, a := range alternatives {
		if s.redact(a.Action.Image) != a.Action.Image || s.redact(a.Action.Container) != a.Action.Container {
			return "", inv.ErrInvalid
		}
		if !evidenceValid(current, a.Evidence) {
			return "", inv.ErrInvalid
		}
		if err := a.Validate(current.Cluster); err != nil {
			return "", err
		}
	}
	rc, found, err := s.retry(session, "submit", key, struct {
		ID           string
		Alternatives []inv.Alternative
	}{id, alternatives})
	if err != nil {
		return "", err
	}
	if found {
		return rc.Result, nil
	}
	current = clone(current)
	group := inv.ID()
	for _, a := range alternatives {
		a.Rationale = s.redact(a.Rationale)
		a.Verification = s.redact(a.Verification)
		current.Proposals = append(current.Proposals, inv.Proposal{ID: inv.ID(), Revision: 1, Group: group, Author: "agent:" + session, State: "pending", Alternative: a})
	}
	rc.Investigation = id
	rc.Result = group
	if err = s.commit("submitted", &current, &rc, ""); err != nil {
		return "", err
	}
	return group, nil
}

// List returns detached snapshots in stable creation order.
func (s *Store) List() []inv.Investigation {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]inv.Investigation, 0, len(s.investigations))
	for _, current := range s.investigations {
		result = append(result, clone(current))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Created.Equal(result[j].Created) {
			return result[i].ID < result[j].ID
		}
		return result[i].Created.Before(result[j].Created)
	})
	return result
}

// Index is the lightweight operator inbox. Agent prose, captures, reviews and
// outcomes are fetched only for the selected investigation via Get.
func (s *Store) Index() []inv.Investigation {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]inv.Investigation, 0, len(s.investigations))
	for _, current := range s.investigations {
		if len(current.Proposals) == 0 {
			continue
		}
		entry := inv.Investigation{ID: current.ID, Context: current.Context, Cluster: current.Cluster, Created: current.Created}
		for _, p := range current.Proposals {
			entry.Proposals = append(entry.Proposals, inv.Proposal{
				ID: p.ID, Revision: p.Revision, Group: p.Group, State: p.State,
				Alternative: inv.Alternative{Target: p.Alternative.Target, Action: inv.Action{Kind: p.Alternative.Action.Kind}},
			})
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Created.Equal(result[j].Created) {
			return result[i].ID < result[j].ID
		}
		return result[i].Created.Before(result[j].Created)
	})
	return result
}

// Active avoids repeatedly cloning idle history and evidence during monitoring.
func (s *Store) Active() []inv.Investigation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []inv.Investigation
	for _, current := range s.investigations {
		for _, op := range current.Executions {
			if op.State == "accepted" || op.State == "unknown" {
				result = append(result, clone(current))
				break
			}
		}
	}
	return result
}
