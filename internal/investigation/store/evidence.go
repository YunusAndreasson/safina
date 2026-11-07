package store

import (
	"strings"
	"time"
	"unicode/utf8"

	inv "github.com/yunus/safina/internal/investigation"
)

// Capture persists only bounded, already selected data, and redacts again before
// storage. Evidence belongs to exactly one pinned investigation.
func (s *Store) Capture(id string, evidence inv.Evidence) (inv.Evidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	original, ok := s.investigations[id]
	if !ok {
		return inv.Evidence{}, inv.ErrNotFound
	}
	if len(original.Evidence) >= 64 {
		return inv.Evidence{}, inv.ErrRecording
	}
	if len(evidence.Text) > 32<<10 {
		return inv.Evidence{}, inv.ErrInvalid
	}
	current := clone(original)
	evidence.ID = inv.ID()
	evidence.Cluster = current.Cluster
	evidence.Observed = time.Now().UTC()
	evidence.Text = s.redact(strings.ToValidUTF8(evidence.Text, "\uFFFD"))
	if len(evidence.Text) > 16<<10 {
		evidence.Text = evidence.Text[:16<<10]
		for !utf8.ValidString(evidence.Text) {
			evidence.Text = evidence.Text[:len(evidence.Text)-1]
		}
		evidence.Truncated = true
	}
	for i := range evidence.Warnings {
		evidence.Warnings[i] = s.redact(evidence.Warnings[i])
	}
	current.Evidence = append(current.Evidence, evidence)
	if err := s.commit("evidence", &current, nil, ""); err != nil {
		return inv.Evidence{}, err
	}
	return evidence, nil
}

func evidenceValid(current inv.Investigation, refs []string) bool {
	for _, ref := range refs {
		found := false
		for _, e := range current.Evidence {
			if e.ID == ref && e.Cluster == current.Cluster {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
