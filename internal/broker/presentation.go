package broker

import inv "github.com/yunus/safina/internal/investigation"

// Reapply the current policy to old journal data. Redaction can expand a
// truncated token, so enforce the byte bound after filtering on every return.
func (s *server) publicEvidence(e inv.Evidence) inv.Evidence {
	text, cut := boundedText(s.redactText(e.Text), evidenceBytes)
	e.Text = text
	e.Truncated = e.Truncated || cut
	for i := range e.Warnings {
		e.Warnings[i] = s.redactText(e.Warnings[i])
	}
	return e
}

// Callers supply detached store snapshots; presentation never modifies the
// authoritative proposals or the broker's executable review cache.
func (s *server) publicInvestigation(current inv.Investigation) inv.Investigation {
	current.Summary = s.redactText(current.Summary)
	for i := range current.Evidence {
		current.Evidence[i] = s.publicEvidence(current.Evidence[i])
	}
	for i := range current.Proposals {
		a := &current.Proposals[i].Alternative
		a.Rationale = s.redactText(a.Rationale)
		a.Verification = s.redactText(a.Verification)
		a.Action.Image = s.redactText(a.Action.Image)
		a.Action.Container = s.redactText(a.Action.Container)
	}
	for i := range current.Executions {
		current.Executions[i].Claim = ""
		current.Executions[i].Detail = s.redactText(current.Executions[i].Detail)
	}
	for i := range current.Decisions {
		if r := current.Decisions[i].Prepared; r != nil {
			r.Patch = nil
			r.Diff = s.redactText(r.Diff)
			r.History = s.redactText(r.History)
			r.Action.Image = s.redactText(r.Action.Image)
			r.Action.Container = s.redactText(r.Action.Container)
		}
	}
	return current
}
