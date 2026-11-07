package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Capture struct {
	mu        sync.Mutex
	store     *Store
	op        *Operation
	id        string
	file      *os.File
	size      int
	pending   string
	discard   bool
	truncated bool
	inPrivate bool
	full      bool
	closed    bool
}

type EvidenceLine struct {
	Observed time.Time  `json:"observed"`
	Source   *time.Time `json:"source,omitempty"`
	Text     string     `json:"text"`
}

func (s *Store) Capture(op *Operation) *Capture {
	if !s.EvidenceEnabled() {
		return nil
	}
	c := &Capture{store: s, op: op, id: ID()}
	dir := filepath.Join(s.Directory(), "evidence")
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil
	}
	quota, err := s.quotaLock()
	if err == nil {
		err = s.capacity("evidence", CaptureBytes)
	}
	if err != nil {
		s.fail(err)
		s.mu.Unlock()
		if quota != nil {
			_ = quota.Close()
		}
		op.Record("capture_unavailable", "evidence capacity or storage unavailable", nil)
		return nil
	}
	err = os.MkdirAll(dir, 0o700)
	if err == nil {
		c.file, err = os.OpenFile(filepath.Join(dir, c.id+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- Generated capture ID beneath operator-configured history root.
	}
	if err == nil {
		err = unix.Flock(int(c.file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err == nil {
		if s.captures == nil {
			s.captures = make(map[*Capture]struct{})
		}
		s.captures[c] = struct{}{}
	}
	s.mu.Unlock()
	_ = quota.Close()
	if err != nil {
		if c.file != nil {
			_ = c.file.Close()
		}
		s.mu.Lock()
		s.fail(err)
		s.mu.Unlock()
		op.Record("capture_unavailable", "could not open evidence file", nil)
		return nil
	}
	s.Append(Record{Target: op.Target, Operation: op.ID, Action: op.Action, Outcome: "capture_started", Evidence: c.id})
	return c
}

func (c *Capture) Append(chunk string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.full {
		return
	}
	for len(chunk) > 0 {
		at := strings.IndexByte(chunk, '\n')
		part := chunk
		if at >= 0 {
			part = chunk[:at]
		}
		if !c.discard {
			if len(c.pending)+len(part) > LineBytes {
				c.pending = "[oversized line omitted]"
				c.discard = true
				c.truncated = true
			} else {
				c.pending += part
			}
		}
		if at < 0 {
			break
		}
		c.writeLine(c.pending)
		c.pending = ""
		c.discard = false
		chunk = chunk[at+1:]
	}
}

func (c *Capture) writeLine(line string) {
	if strings.Contains(line, "-----BEGIN") && strings.Contains(line, "PRIVATE KEY-----") {
		c.inPrivate = true
	}
	if c.inPrivate {
		ending := strings.Contains(line, "-----END") && strings.Contains(line, "PRIVATE KEY-----")
		line = "[REDACTED PRIVATE KEY]"
		if ending {
			c.inPrivate = false
		}
	}
	var source *time.Time
	if first, _, ok := strings.Cut(line, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, first); err == nil {
			source = &t
		}
	}
	b, err := json.Marshal(EvidenceLine{Observed: time.Now().UTC(), Source: source, Text: c.store.Redact(line)})
	if err != nil {
		return
	}
	b = append(b, '\n')
	if c.size+len(b) > CaptureBytes {
		c.truncated = true
		c.full = true
		return
	}
	n, err := c.file.Write(b)
	c.size += n
	if err != nil {
		c.truncated = true
		c.store.mu.Lock()
		c.store.fail(err)
		c.store.mu.Unlock()
	}
}

func (c *Capture) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.pending != "" {
		c.writeLine(c.pending)
	}
	err := c.file.Sync()
	if e := c.file.Close(); err == nil {
		err = e
	}
	outcome := "capture_ended"
	if c.truncated {
		outcome = "capture_truncated"
	}
	if err != nil {
		outcome = "capture_failed"
		c.store.mu.Lock()
		c.store.fail(err)
		c.store.mu.Unlock()
	}
	c.store.mu.Lock()
	delete(c.store.captures, c)
	c.store.mu.Unlock()
	c.store.Append(Record{Target: c.op.Target, Operation: c.op.ID, Action: c.op.Action, Outcome: outcome, Evidence: c.id})
}
