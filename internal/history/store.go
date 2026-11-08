// Package history keeps a local, versioned journal of Safina activity.
// Records are evidence of observation, not a cluster-wide or tamper-proof audit.
package history

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	CaptureBytes = 10 << 20
	LineBytes    = 256 << 10
)

type Options struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Directory      string   `json:"directory,omitempty"`
	Evidence       *bool    `json:"evidence,omitempty"`
	ActionDays     int      `json:"actionDays,omitempty"`
	EvidenceDays   int      `json:"evidenceDays,omitempty"`
	ActionBytes    int64    `json:"actionBytes,omitempty"`
	EvidenceBytes  int64    `json:"evidenceBytes,omitempty"`
	RedactPatterns []string `json:"redactPatterns,omitempty"`
}

func (o Options) Defaults() Options {
	if o.Directory == "" {
		home, _ := os.UserHomeDir()
		o.Directory = filepath.Join(home, ".safina", "history")
	}
	if o.ActionDays <= 0 {
		o.ActionDays = 90
	}
	if o.EvidenceDays <= 0 {
		o.EvidenceDays = 14
	}
	if o.ActionBytes <= 0 {
		o.ActionBytes = 256 << 20
	}
	if o.EvidenceBytes <= 0 {
		o.EvidenceBytes = 1 << 30
	}
	return o
}

type Target struct {
	WorkloadKind string    `json:"workloadKind,omitempty"`
	WorkloadName string    `json:"workloadName,omitempty"`
	WorkloadUID  types.UID `json:"workloadUID,omitempty"`
	Cluster      string    `json:"cluster,omitempty"`
	Context      string    `json:"context,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Kind         string    `json:"kind,omitempty"`
	Name         string    `json:"name,omitempty"`
	UID          types.UID `json:"uid,omitempty"`
	Container    string    `json:"container,omitempty"`
}

type Record struct {
	Investigation string            `json:"investigation,omitempty"`
	Proposal      string            `json:"proposal,omitempty"`
	Revision      int               `json:"revision,omitempty"`
	Decision      string            `json:"decision,omitempty"`
	CoverageGap   bool              `json:"coverageGap,omitempty"`
	Version       int               `json:"version"`
	ID            string            `json:"id"`
	Session       string            `json:"session"`
	Sequence      uint64            `json:"sequence"`
	Time          time.Time         `json:"time"`
	SourceTime    *time.Time        `json:"sourceTime,omitempty"`
	Operation     string            `json:"operation,omitempty"`
	Target        Target            `json:"target"`
	Action        string            `json:"action"`
	Outcome       string            `json:"outcome"`
	Detail        string            `json:"detail,omitempty"`
	Parameters    map[string]string `json:"parameters,omitempty"`
	Evidence      string            `json:"evidence,omitempty"`
}

type Store struct {
	mu         sync.Mutex
	opts       Options
	session    string
	seq        uint64
	db         *sql.DB
	patterns   []*regexp.Regexp
	health     string
	incomplete bool
	closed     bool
	closing    bool
	captures   map[*Capture]struct{}
	lastPrune  time.Time
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// ClusterID intentionally contains neither credentials nor the endpoint itself.
func ClusterID(endpoint string, ca []byte) string {
	normalized := strings.TrimRight(endpoint, "/")
	if u, err := url.Parse(normalized); err == nil {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.User = nil
		u.Fragment = ""
		normalized = u.String()
	}
	sum := sha256.Sum256(append([]byte(normalized+"\x00"), ca...))
	return hex.EncodeToString(sum[:16])
}

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)((?:password|passwd|token|api[_-]?key|secret|authorization)["']?\s*[:=]\s*["']?)[^\s,"'}]+`),
	regexp.MustCompile(`-----BEGIN[^\n]*PRIVATE KEY-----[\s\S]*?-----END[^\n]*PRIVATE KEY-----`),
	regexp.MustCompile(`(://)[^\s/@:]+:[^\s/@]+@`),
}

func Open(opts Options) (*Store, error) {
	opts = opts.Defaults()
	s := &Store{opts: opts, session: ID()}
	if opts.Enabled != nil && !*opts.Enabled {
		s.closed = true
		return s, nil
	}
	for _, p := range opts.RedactPatterns {
		r, err := regexp.Compile(p)
		if err != nil {
			s.fail(err)
			s.closed = true // Invalid redaction configuration must never silently collect evidence.
			return s, err
		}
		s.patterns = append(s.patterns, r)
	}
	if err := os.MkdirAll(opts.Directory, 0o700); err != nil {
		s.fail(err)
		return s, err
	}
	var err error
	s.db, err = openDatabase(opts.Directory, false)
	if err == nil {
		err = s.importLegacy()
	}
	if err != nil {
		s.fail(err)
		if s.db != nil {
			_ = s.db.Close()
			s.db = nil
		}
		return s, err
	}
	if err := s.Prune(); err != nil {
		s.fail(err)
		return s, err
	}
	s.Append(Record{Action: "session", Outcome: "started", Parameters: map[string]string{"coverage": "Safina actions only; shell commands and external activity excluded", "evidence": strconv.FormatBool(opts.Evidence == nil || *opts.Evidence), "actionDays": strconv.Itoa(opts.ActionDays), "evidenceDays": strconv.Itoa(opts.EvidenceDays)}})
	return s, nil
}

func (s *Store) Directory() string {
	if s == nil {
		return ""
	}
	return s.opts.Directory
}

func (s *Store) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.closing
}

func (s *Store) EvidenceEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && !s.closing && (s.opts.Evidence == nil || *s.opts.Evidence)
}

func (s *Store) Health() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

func (s *Store) fail(err error) { s.health = "Logbook incomplete: " + err.Error(); s.incomplete = true }

func (s *Store) Redact(text string) string {
	for i, r := range credentialPatterns {
		replacement := "[REDACTED]"
		if i != 2 {
			replacement = "${1}[REDACTED]"
		}
		text = r.ReplaceAllString(text, replacement)
	}
	if s != nil {
		for _, r := range s.patterns {
			text = r.ReplaceAllString(text, "[REDACTED]")
		}
	}
	return text
}

// Append syncs lifecycle records before returning. Call from command workers for
// network operations; recording failure is fail-open and remains visible in Health.
func (s *Store) Append(r Record) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ""
	}
	s.seq++
	r.CoverageGap = s.incomplete
	r.Version = 1
	r.ID = ID()
	r.Session = s.session
	r.Sequence = s.seq
	if r.Time.IsZero() {
		r.Time = time.Now().UTC()
	}
	r.Detail = s.Redact(r.Detail)
	if len(r.Detail) > LineBytes {
		r.Detail = r.Detail[:LineBytes] + " [truncated]"
	}
	if r.Parameters != nil {
		p := make(map[string]string, len(r.Parameters))
		for k, v := range r.Parameters {
			p[k] = s.Redact(v)
		}
		r.Parameters = p
	}
	data, err := json.Marshal(r)
	if err != nil {
		s.fail(err)
		return ""
	}
	if err = s.writeRecord(r, data); err != nil {
		s.fail(err)
		return ""
	}

	if s.incomplete {
		s.health = "Logbook incomplete; latest action saved, but earlier recording failures remain"
	}
	return r.ID
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed || s.closing {
		s.mu.Unlock()
		return nil
	}
	s.closing = true
	captures := make([]*Capture, 0, len(s.captures))
	for c := range s.captures {
		captures = append(captures, c)
	}
	s.mu.Unlock()
	for _, c := range captures {
		c.Close()
	}
	s.Append(Record{Action: "session", Outcome: "ended"})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			s.fail(err)
		}
	}

	if s.incomplete {
		return errors.New(s.health)
	}
	return nil
}

func (s *Store) Begin(t Target, action string, parameters map[string]string) *Operation {
	op := &Operation{Store: s, Target: t, Action: action, ID: ID()}
	op.Record("requested", "", parameters)
	return op
}

type Operation struct {
	Store                       *Store
	Target                      Target
	Action, ID                  string
	Generation, ResourceVersion string
}

func (o *Operation) Record(outcome, detail string, params map[string]string) {
	if o != nil {
		o.Store.Append(Record{Target: o.Target, Action: o.Action, Operation: o.ID, Outcome: outcome, Detail: detail, Parameters: params})
	}
}

func (o *Operation) Finish(err error, success string) {
	if err == nil {
		o.Record(success, "", nil)
		return
	}
	// Errors may echo request payloads. Keep free-form bodies out of action history.
	outcome := "failed"
	if errors.Is(err, context.Canceled) {
		outcome = "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		outcome = "timeout"
	}
	detail := fmt.Sprintf("%T; free-form error body omitted", err)
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		v := status.Status()
		detail = fmt.Sprintf("Kubernetes status %d: %s", v.Code, v.Reason)
	}
	if outcome == "timeout" || outcome == "cancelled" {
		detail += "; server-side outcome may be unknown"
	}
	o.Record(outcome, detail, nil)
}

type operationKey struct{}

func WithOperation(ctx context.Context, op *Operation) context.Context {
	return context.WithValue(ctx, operationKey{}, op)
}

func FromContext(ctx context.Context) *Operation {
	op, _ := ctx.Value(operationKey{}).(*Operation)
	return op
}
