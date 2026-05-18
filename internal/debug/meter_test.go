package debug

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestMeter_DropsCallsWhenInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	m := NewMeter()
	client := &http.Client{Transport: &MeteringTransport{Base: http.DefaultTransport, Meter: m}}

	// Inactive — no record.
	resp := doGet(t, client, srv.URL)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assert.Empty(t, m.Snapshot())

	// Active — record landed.
	m.Start()
	resp = doGet(t, client, srv.URL)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	m.Stop()
	recs := m.Snapshot()
	require.Len(t, recs, 1)
	assert.Equal(t, http.MethodGet, recs[0].Verb)
	assert.Equal(t, 200, recs[0].Status)
	assert.Equal(t, int64(2), recs[0].Bytes)
}

func TestMeter_ChunkedBytesCountedOnClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Force chunked by writing without Content-Length and flushing.
		fl, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("hello "))
		if fl != nil {
			fl.Flush()
		}
		_, _ = w.Write([]byte("world"))
	}))
	defer srv.Close()

	m := NewMeter()
	m.Start()
	client := &http.Client{Transport: &MeteringTransport{Base: http.DefaultTransport, Meter: m}}
	resp := doGet(t, client, srv.URL)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "hello world", string(body))
	m.Stop()
	recs := m.Snapshot()
	require.Len(t, recs, 1)
	assert.Equal(t, int64(len("hello world")), recs[0].Bytes)
}

func TestMeter_ResetOnStart(t *testing.T) {
	m := NewMeter()
	m.record(CallRecord{Verb: "X"})
	m.Start()
	assert.Empty(t, m.Snapshot())
}

func TestMeter_ConcurrentRecord(t *testing.T) {
	m := NewMeter()
	m.Start()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.record(CallRecord{Verb: "GET"})
		}()
	}
	wg.Wait()
	m.Stop()
	assert.Len(t, m.Snapshot(), 50)
}

func TestWrapWithMeter_NonNilChain(t *testing.T) {
	m := NewMeter()
	wrap := WrapWithMeter(m)
	rt := wrap(http.DefaultTransport)
	mt, ok := rt.(*MeteringTransport)
	require.True(t, ok)
	assert.Equal(t, m, mt.Meter)
	assert.NotNil(t, mt.Base)
}

func TestMeter_PathRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := NewMeter()
	m.Start()
	client := &http.Client{Transport: &MeteringTransport{Base: http.DefaultTransport, Meter: m}}
	resp := doGet(t, client, srv.URL+"/api/v1/pods")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	m.Stop()
	recs := m.Snapshot()
	require.Len(t, recs, 1)
	assert.True(t, strings.HasSuffix(recs[0].Path, "/api/v1/pods"))
}
