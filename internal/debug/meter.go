package debug

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// CallRecord is one observed HTTP round-trip against the Kubernetes API.
// Captured by MeteringTransport while a Meter is active; consumed by the
// bench harness for per-scenario percentile / byte-count aggregation.
//
// ContentType is captured because it answers "did the variant's negotiated
// encoding actually engage?" (e.g. protobuf vs JSON). The server's response
// Content-Type is what was actually served, regardless of what we asked for.
type CallRecord struct {
	Verb        string
	Path        string
	Status      int
	Duration    time.Duration
	Bytes       int64
	ContentType string
	Err         string
}

// Meter is a thread-safe collector for CallRecords. Zero value is ready.
// One Meter is shared across all transports for a single scenario iteration;
// callers Reset() between iterations and Snapshot() at the end.
//
// Active() reports whether records should be recorded — when false the
// MeteringTransport short-circuits with a single atomic load, so an installed
// transport on an idle client costs essentially nothing.
type Meter struct {
	active atomic.Bool
	mu     sync.Mutex
	recs   []CallRecord
}

func NewMeter() *Meter { return &Meter{} }

func (m *Meter) Start() {
	m.mu.Lock()
	m.recs = m.recs[:0]
	m.mu.Unlock()
	m.active.Store(true)
}

func (m *Meter) Stop() { m.active.Store(false) }

func (m *Meter) Active() bool { return m.active.Load() }

// Snapshot returns a copy of the recorded calls. Safe to call while active —
// the slice is copied under the lock so subsequent appends don't race readers.
func (m *Meter) Snapshot() []CallRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CallRecord, len(m.recs))
	copy(out, m.recs)
	return out
}

func (m *Meter) record(r CallRecord) {
	m.mu.Lock()
	m.recs = append(m.recs, r)
	m.mu.Unlock()
}

// MeteringTransport wraps an http.RoundTripper, recording one CallRecord per
// request into the bound Meter while it is Active(). Bytes are counted on the
// response body via a wrapped io.ReadCloser — this defers the cost to where
// the caller would have paid it anyway (reading the body) instead of buffering
// the response.
//
// Unlike AuditTransport this is intended for the benchmark harness, not the
// running TUI: it pins per-request allocations into the Meter regardless of
// SAFINA_DEBUG.
type MeteringTransport struct {
	Base  http.RoundTripper
	Meter *Meter
}

// WrapWithMeter is the rest.Config.WrapTransport-compatible factory.
func WrapWithMeter(m *Meter) func(http.RoundTripper) http.RoundTripper {
	return func(rt http.RoundTripper) http.RoundTripper {
		return &MeteringTransport{Base: rt, Meter: m}
	}
}

func (t *MeteringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Meter == nil || !t.Meter.Active() {
		return t.Base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		t.Meter.record(CallRecord{
			Verb:     req.Method,
			Path:     req.URL.Path,
			Duration: time.Since(start),
			Err:      err.Error(),
		})
		return resp, err
	}
	rec := CallRecord{
		Verb:        req.Method,
		Path:        req.URL.Path,
		Status:      resp.StatusCode,
		Duration:    time.Since(start),
		ContentType: resp.Header.Get("Content-Type"),
	}
	if resp.ContentLength > 0 {
		rec.Bytes = resp.ContentLength
		t.Meter.record(rec)
		return resp, nil
	}
	// Chunked / unknown length — count bytes as the caller reads them and
	// finalize the record on Close. We attribute the full read duration to
	// the request (start → close), which matches what the caller actually
	// observes as latency.
	resp.Body = &countingBody{
		inner: resp.Body,
		meter: t.Meter,
		rec:   rec,
		start: start,
	}
	return resp, nil
}

type countingBody struct {
	inner io.ReadCloser
	meter *Meter
	rec   CallRecord
	start time.Time
	n     int64
	done  bool
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.inner.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingBody) Close() error {
	err := c.inner.Close()
	if !c.done {
		c.done = true
		c.rec.Bytes = c.n
		c.rec.Duration = time.Since(c.start)
		c.meter.record(c.rec)
	}
	return err
}
