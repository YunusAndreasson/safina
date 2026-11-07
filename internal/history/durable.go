package history

import (
	"encoding/json"
	"errors"
	"strings"
)

// AppendDurable delivers a broker-owned stable event ID. Unlike Append it
// returns recording errors and preserves identity across retries.
func (s *Store) AppendDurable(r Record) error {
	if s == nil {
		return errors.New("logbook unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing {
		return errors.New("logbook disabled or closed")
	}
	if len(r.ID) != 32 || strings.Trim(r.ID, "0123456789abcdef") != "" || r.Version != 1 || r.Time.IsZero() {
		return errors.New("invalid stable history record")
	}
	// Round-trip to avoid mutating caller-owned parameter maps.
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &r); err != nil {
		return err
	}
	r.Detail = s.Redact(r.Detail)
	for key, value := range r.Parameters {
		r.Parameters[key] = s.Redact(value)
	}
	data, err = json.Marshal(r)
	if err != nil {
		return err
	}
	if len(data) > LineBytes {
		return errors.New("history event exceeds limit")
	}
	return s.writeRecord(r, data)
}
