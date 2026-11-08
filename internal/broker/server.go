// Package broker hosts local investigation state independently of the TUI.
package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yunus/safina/internal/broker/api"
	"github.com/yunus/safina/internal/history"
	inv "github.com/yunus/safina/internal/investigation"
	"github.com/yunus/safina/internal/investigation/store"
	"github.com/yunus/safina/internal/localstate"
)

const (
	requestBytes   = 256 << 10
	requestTimeout = 15 * time.Second
	// Leave response-write headroom beyond the client request deadline.
	responseTimeout = requestTimeout + 5*time.Second
)

type Options struct {
	RuntimeDir string
	StateDir   string
	Kubeconfig string
	History    history.Options
	// Resolve is a read-only kubeconfig test seam. Only operator preparation,
	// approval, and recovery construct Kubernetes clients.
	Resolve func(string) (string, error)
}

func (o Options) Defaults() Options {
	home, _ := os.UserHomeDir()
	if o.RuntimeDir == "" {
		o.RuntimeDir = filepath.Join(home, ".safina", "run")
	}
	if o.StateDir == "" {
		o.StateDir = filepath.Join(home, ".safina", "investigations")
	}
	for _, path := range []*string{&o.RuntimeDir, &o.StateDir, &o.Kubeconfig} {
		if *path != "" {
			if absolute, err := filepath.Abs(*path); err == nil {
				*path = absolute
			}
		}
	}
	return o
}

func Socket(o Options) string { return filepath.Join(o.Defaults().RuntimeDir, "broker.sock") }

type Status struct {
	Version     int      `json:"version"`
	PID         int      `json:"pid"`
	StateDir    string   `json:"stateDir"`
	Kubeconfig  string   `json:"kubeconfig"`
	Warnings    []string `json:"warnings,omitempty"`
	Undelivered int      `json:"undelivered"`
}

type server struct {
	reviewMu      sync.Mutex
	reviews       map[string]inv.Review
	opts          Options
	store         *store.Store
	history       *history.Store
	mu            sync.Mutex
	deliveryError string
	stop          chan struct{}
	stopOnce      sync.Once
}

// Serve holds both runtime ownership and state writer locks until all handlers
// stop. A second process cannot unlink the active socket or write the journal.
func Serve(ctx context.Context, options Options) error {
	o := options.Defaults()
	redact, err := history.NewRedactor(o.History.RedactPatterns)
	if err != nil {
		return err
	}
	if err = localstate.Directory(o.RuntimeDir); err != nil {
		return err
	}
	runtimeLock, err := localstate.Lock(filepath.Join(o.RuntimeDir, "broker.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = runtimeLock.Close() }()
	journal, historyErr := history.Open(o.History)
	defer func() { _ = journal.Close() }()
	st, err := store.OpenRedacted(o.StateDir, redact)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	s := &server{opts: o, store: st, history: journal, stop: make(chan struct{})}
	if historyErr != nil {
		s.deliveryError = "Logbook unavailable; delivery pending"
	}
	if o.Resolve == nil {
		s.opts.Resolve = func(alias string) (string, error) { return resolveCluster(o.Kubeconfig, alias) }
	}
	path := Socket(o)
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace a non-socket runtime entry")
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	if err = os.Chmod(path, 0o600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/investigation", s.investigation)
	mux.HandleFunc("POST /v1/operator", s.operator)
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, _ *http.Request) {
		s.stopOnce.Do(func() { close(s.stop) })
		w.WriteHeader(http.StatusAccepted)
	})
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: responseTimeout, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(ownerListener{listener}) }()
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() { defer close(monitorDone); s.monitor(monitorCtx) }()
	defer func() { stopMonitor(); <-monitorDone }()
	s.deliver()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return shutdown(ctx, httpServer, done)
		case <-s.stop:
			return shutdown(ctx, httpServer, done)
		case err = <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ticker.C:
			s.deliver()
		}
	}
}

func shutdown(parent context.Context, s *http.Server, done <-chan error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 15*time.Second)
	defer cancel()
	err := s.Shutdown(ctx)
	if err != nil {
		_ = s.Close()
	}
	serveErr := <-done
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(err, serveErr)
}

func (s *server) deliver() {
	err := s.store.Deliver(s.history.AppendDurable)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.deliveryError = "Logbook delivery incomplete; pending events retained"
	} else {
		s.deliveryError = ""
	}
}

func (s *server) health() ([]string, int) {
	warnings, pending := s.store.Health()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deliveryError != "" {
		warnings = append(warnings, s.deliveryError)
	}
	return warnings, pending
}

func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	warnings, pending := s.health()
	writeJSON(w, Status{Version: api.Version, PID: os.Getpid(), StateDir: s.opts.StateDir, Kubeconfig: s.opts.Kubeconfig, Warnings: warnings, Undelivered: pending})
}

func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("%w: malformed or unsupported fields", inv.ErrInvalid)
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return inv.ErrInvalid
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "Could not encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Once headers are sent, a disconnected client cannot receive an error response.
	if _, err := w.Write(append(data, '\n')); err != nil {
		return
	}
}

func (s *server) investigation(w http.ResponseWriter, r *http.Request) {
	var request api.Request
	response := api.Response{Version: api.Version}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBytes))
	if err == nil {
		err = decode(body, &request)
	}
	response.ID = request.ID
	var result any
	if err == nil && (request.Version != api.Version || request.ID == "") {
		response.Error = &api.Error{Code: "unsupported_version", Message: "version 1 and requestId required"}
	} else if err == nil {
		result, err = s.dispatchContext(r.Context(), request)
	}
	if err != nil {
		response.Error = publicError(err)
	}
	if response.Error == nil {
		response.Result, err = json.Marshal(result)
		if err != nil {
			response.Error = publicError(err)
		}
	}
	writeJSON(w, response)
}

func publicError(err error) *api.Error {
	code := "unavailable"
	switch {
	case errors.Is(err, inv.ErrInvalid):
		code = "invalid_argument"
	case errors.Is(err, inv.ErrNotFound):
		code = "not_found"
	case errors.Is(err, inv.ErrConflict):
		code = "conflict"
	case errors.Is(err, inv.ErrRecording):
		code = "recording_unavailable"
	}
	// Do not disclose kubeconfig paths, credentials or external error payloads.
	return &api.Error{Code: code, Message: code}
}

func (s *server) view(current inv.Investigation) api.View {
	current = s.publicInvestigation(current)
	warnings, pending := s.health()
	warnings = append(warnings, "Agent statements are unverified. Captured evidence is scoped, bounded and untrusted; controller completion is not application recovery.")
	return api.View{Investigation: current, Warnings: warnings, Undelivered: pending}
}

func (s *server) dispatchContext(ctx context.Context, r api.Request) (any, error) {
	switch r.Method {
	case "list_contexts":
		var p api.Empty
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.contexts()
	case "inspect_workload":
		var p api.InspectRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.inspect(ctx, p)
	case "read_events":
		var p api.EventsRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.events(ctx, p)
	case "read_logs":
		var p api.LogsRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.logs(ctx, p)
	case "query_history":
		var p api.HistoryRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.historyQuery(p)
	case "get_evidence":
		var p api.EvidenceRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		return s.evidence(p)
	case "start_investigation":
		var p api.StartRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		if p.Context == "" {
			return nil, inv.ErrInvalid
		}
		cluster, err := s.opts.Resolve(p.Context)
		if err != nil {
			return nil, err
		}
		current, err := s.store.Start(inv.Investigation{Session: p.Session, Cluster: cluster, Context: p.Context, Target: p.Target, Summary: p.Summary}, p.Key)
		if err != nil {
			return nil, err
		}
		view := s.view(current)
		view.History = s.historyBrief(current, current.Target)
		return view, nil
	case "submit_proposals":
		var p api.SubmitRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		group, err := s.store.Submit(p.Investigation, p.Session, p.Key, p.Alternatives)
		return api.Submitted{Group: group}, err
	case "get_investigation":
		var p api.GetRequest
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		current, err := s.store.Get(p.Investigation)
		if err != nil {
			return nil, err
		}
		return s.view(current), nil
	default:
		return nil, inv.ErrInvalid
	}
}
