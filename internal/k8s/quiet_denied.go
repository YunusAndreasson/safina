package k8s

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"time"
)

// quietDenied sits under the informer clientset. A reflector whose kind this
// account may not list (403), or the server doesn't serve (404), retries its
// List and Watch forever with a backoff capped at about 30s; client-go has no
// way to stop one informer of a shared factory. So for unavailableFor after
// such an answer, a GET to the same path gets that answer replayed from
// memory instead of going to the apiserver. Then one request goes through
// again, which picks up RBAC or a server that changed meanwhile.
type quietDenied struct {
	next   http.RoundTripper
	denied sync.Map // URL path → deniedAnswer
}

type deniedAnswer struct {
	status int
	header http.Header
	body   []byte
	until  time.Time
}

func (q *quietDenied) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return q.next.RoundTrip(req)
	}
	key := req.URL.Path
	if v, ok := q.denied.Load(key); ok {
		if d, ok := v.(deniedAnswer); ok && time.Now().Before(d.until) {
			return &http.Response{
				Status:        http.StatusText(d.status),
				StatusCode:    d.status,
				Proto:         "HTTP/1.1",
				ProtoMajor:    1,
				ProtoMinor:    1,
				Header:        d.header.Clone(),
				Body:          io.NopCloser(bytes.NewReader(d.body)),
				ContentLength: int64(len(d.body)),
				Request:       req,
			}, nil
		}
	}
	resp, err := q.next.RoundTrip(req)
	if err != nil || (resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound) {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	q.denied.Store(key, deniedAnswer{status: resp.StatusCode, header: resp.Header.Clone(), body: body, until: time.Now().Add(unavailableFor)})
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}
