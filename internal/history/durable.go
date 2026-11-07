package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yunus/safina/internal/localstate"
	"golang.org/x/sys/unix"
)

// AppendDurable delivers a broker-owned stable event ID. Unlike Append it
// returns recording errors and preserves identity across retries. Legacy query,
// retention and export paths consume the resulting ordinary v1 JSONL record.
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
	data = append(data, '\n')
	quota, err := s.quotaLock()
	if err != nil {
		return err
	}
	defer func() { _ = quota.Close() }()
	actions := filepath.Join(s.opts.Directory, "actions")
	dir := filepath.Join(actions, "broker")
	if err = localstate.Directory(actions); err != nil {
		return err
	}
	if err = localstate.Directory(dir); err != nil {
		return err
	}
	if err = localstate.SyncDirectory(s.opts.Directory); err != nil {
		return err
	}
	if err = localstate.SyncDirectory(actions); err != nil {
		return err
	}
	path := filepath.Join(dir, r.ID+".jsonl")
	existing, err := localstate.Open(path, unix.O_RDONLY)
	if err == nil {
		defer func() { _ = existing.Close() }()
		saved, readErr := io.ReadAll(io.LimitReader(existing, LineBytes+2))
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(saved, data) {
			return errors.New("stable history event ID collision")
		}
		if err = existing.Sync(); err != nil {
			return err
		}
		return localstate.SyncDirectory(dir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = s.capacity("actions", int64(len(data))); err != nil {
		return err
	}
	temp := filepath.Join(dir, r.ID+".pending")
	f, err := localstate.Open(temp, unix.O_CREAT|unix.O_WRONLY|unix.O_TRUNC)
	if err != nil {
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(temp, path); err != nil {
		return err
	}
	return localstate.SyncDirectory(dir)
}
