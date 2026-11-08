package broker

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/broker/api"
	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/YunusAndreasson/safina/internal/localstate"
	"github.com/stretchr/testify/require"
)

// The actual on-demand launcher executes the current binary with `serve`.
// Give the test executable that same entry point to exercise detached startup.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" && os.Getenv("SAFINA_BROKER_TEST_HELPER") == "1" {
		flags := flag.NewFlagSet("serve", flag.ExitOnError)
		var o Options
		flags.StringVar(&o.RuntimeDir, "runtime-dir", "", "runtime")
		flags.StringVar(&o.StateDir, "state-dir", "", "state")
		flags.StringVar(&o.Kubeconfig, "kubeconfig", "", "kubeconfig")
		flags.StringVar(&o.History.Directory, "history-dir", "", "history")
		if err := flags.Parse(os.Args[2:]); err != nil {
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := Serve(ctx, o)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func brokerFixture(t *testing.T) (Options, *atomic.Int32) {
	t.Helper()
	t.Setenv("SAFINA_BROKER_TEST_HELPER", "1")
	calls := &atomic.Int32{}
	cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(cluster.Close)
	// Unix socket paths are limited to roughly 100 bytes on supported platforms.
	runtimeDir, err := os.MkdirTemp("", "safina-test-") //nolint:usetesting // t.TempDir paths can exceed Unix socket path limits.
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(runtimeDir)) })
	root := t.TempDir()
	kubeconfig := filepath.Join(root, "config")
	require.NoError(t, os.WriteFile(kubeconfig, []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: %s\ncontexts:\n- name: explicit\n  context:\n    cluster: cluster\n    user: unused\ncurrent-context: explicit\n", cluster.URL)), 0o600))
	o := Options{RuntimeDir: runtimeDir, StateDir: filepath.Join(root, "state"), Kubeconfig: kubeconfig, History: history.Options{Directory: filepath.Join(root, "history")}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, statusErr := BrokerStatus(ctx, o); statusErr == nil {
			_ = Stop(ctx, o)
			require.Eventually(t, func() bool { _, e := BrokerStatus(ctx, o); return e != nil }, 2*time.Second, 10*time.Millisecond)
			require.Eventually(t, func() bool {
				lock, e := localstate.Lock(filepath.Join(o.RuntimeDir, "broker.lock"))
				if e != nil {
					return false
				}
				_ = lock.Close()
				return true
			}, 3*time.Second, 10*time.Millisecond)
		}
	})
	return o, calls
}

func TestConcurrentAutostartSubmissionAndCrashRecovery(t *testing.T) {
	o, clusterCalls := brokerFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	pids := make(chan int, 12)
	for range 12 {
		wg.Go(func() {
			if err := Ensure(ctx, o); err != nil {
				t.Error(err)
				return
			}
			status, err := BrokerStatus(ctx, o)
			if err != nil {
				t.Error(err)
				return
			}
			pids <- status.PID
		})
	}
	wg.Wait()
	close(pids)
	status, err := BrokerStatus(ctx, o)
	require.NoError(t, err)
	require.NotEqual(t, os.Getpid(), status.PID)
	starterCtx, cancelStarter := context.WithCancel(ctx)
	require.NoError(t, Ensure(starterCtx, o))
	cancelStarter()
	_, err = BrokerStatus(ctx, o)
	require.NoError(t, err, "broker survives initiating client cancellation")
	for pid := range pids {
		require.Equal(t, status.PID, pid)
	}
	client := NewClient(o)
	started, err := client.Start(ctx, api.StartRequest{Session: "claude-a", Key: "start", Context: "explicit", Summary: "token=private"})
	require.NoError(t, err)
	require.NotContains(t, started.Investigation.Summary, "private")
	replicas := int32(3)
	alternative := inv.Alternative{Target: inv.Target{Cluster: started.Investigation.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "payments", Name: "api", UID: "uid"}, Observed: inv.Observed{ResourceVersion: "1", Time: time.Now().UTC()}, Action: inv.Action{Kind: "scale", Replicas: &replicas}, Rationale: "password=private"}
	submitted := api.SubmitRequest{Session: "claude-a", Key: "proposal", Investigation: started.Investigation.ID, Alternatives: []inv.Alternative{alternative}}
	groups := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			result, e := client.Submit(ctx, submitted)
			if e != nil {
				t.Error(e)
				return
			}
			groups <- result.Group
		})
	}
	wg.Wait()
	close(groups)
	group := <-groups
	for g := range groups {
		require.Equal(t, group, g)
	}
	other, err := client.Start(ctx, api.StartRequest{Session: "claude-b", Key: "start", Context: "explicit"})
	require.NoError(t, err)
	require.NotEqual(t, started.Investigation.ID, other.Investigation.ID)
	require.Eventually(t, func() bool { current, e := BrokerStatus(ctx, o); return e == nil && current.Undelivered == 0 }, 4*time.Second, 20*time.Millisecond)
	// Crash the detached process. The next client call starts/reconnects without a TUI.
	process, err := os.FindProcess(status.PID)
	require.NoError(t, err)
	require.NoError(t, process.Kill())
	require.Eventually(t, func() bool { _, e := BrokerStatus(ctx, o); return e != nil }, 3*time.Second, 10*time.Millisecond)
	restored, err := client.Get(ctx, api.GetRequest{Investigation: started.Investigation.ID})
	require.NoError(t, err)
	require.Len(t, restored.Investigation.Proposals, 1)
	require.Equal(t, "pending", restored.Investigation.Proposals[0].State)
	require.NotContains(t, restored.Investigation.Proposals[0].Alternative.Rationale, "private")
	retried, err := client.Submit(ctx, submitted)
	require.NoError(t, err)
	require.Equal(t, group, retried.Group)
	statusAfter, err := BrokerStatus(ctx, o)
	require.NoError(t, err)
	require.NotEqual(t, status.PID, statusAfter.PID)
	require.Zero(t, clusterCalls.Load(), "M1 investigation API must never contact Kubernetes")
	require.NoError(t, Stop(ctx, o))
	require.Eventually(t, func() bool { _, e := BrokerStatus(ctx, o); return e != nil }, 3*time.Second, 10*time.Millisecond)
	// Explicit status/stop do not auto-start.
	_, err = BrokerStatus(ctx, o)
	require.Error(t, err)
	require.Error(t, Stop(ctx, o))
}

func TestInvestigationDispatcherRejectsApprovalAndUnknownFields(t *testing.T) {
	o, clusterCalls := brokerFixture(t)
	require.NoError(t, Ensure(t.Context(), o))
	for _, method := range []string{"approve", "execute", "select", "reject", "override", "stop", "revise"} {
		b, err := json.Marshal(api.Request{Version: 1, ID: "request", Method: method, Params: json.RawMessage(`{}`)})
		require.NoError(t, err)
		var response api.Response
		require.NoError(t, request(t.Context(), o, http.MethodPost, "/v1/investigation", b, &response))
		require.NotNil(t, response.Error)
		require.Equal(t, "invalid_argument", response.Error.Code)
	}
	for _, params := range []string{
		`{"sessionId":"a","idempotencyKey":"a","context":"explicit","approve":true}`,
		`{"sessionId":"a","idempotencyKey":"a","context":"explicit","override":true}`,
		`{"sessionId":"a","idempotencyKey":"a"}`,
	} {
		b, err := json.Marshal(api.Request{Version: 1, ID: "request", Method: "start_investigation", Params: json.RawMessage(params)})
		require.NoError(t, err)
		var response api.Response
		require.NoError(t, request(t.Context(), o, http.MethodPost, "/v1/investigation", b, &response))
		require.NotNil(t, response.Error)
	}
	require.Zero(t, clusterCalls.Load())
}

func TestRuntimePermissionsAndConfigurationMismatch(t *testing.T) {
	o, _ := brokerFixture(t)
	require.NoError(t, Ensure(t.Context(), o))
	for _, path := range []string{o.RuntimeDir, o.StateDir} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
	for _, path := range []string{Socket(o), filepath.Join(o.StateDir, "journal.jsonl")} {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	changed := o
	changed.StateDir = filepath.Join(t.TempDir(), "other")
	require.ErrorContains(t, Ensure(t.Context(), changed), "configuration differs")
	require.Error(t, Serve(t.Context(), o), "second server must not take the socket")
	_, err := BrokerStatus(t.Context(), o)
	require.NoError(t, err)
}

func TestInvalidRedactionBlocksStartupEvenWithHistoryDisabled(t *testing.T) {
	enabled := false
	o := Options{History: history.Options{Enabled: &enabled, RedactPatterns: []string{"["}}}
	require.Error(t, Serve(t.Context(), o))
}

func TestStartupWaitsForPreviousRuntimeOwner(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("", "safina-handoff-") //nolint:usetesting // Unix socket paths must stay below platform limits.
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(runtimeDir)) })
	o := Options{RuntimeDir: runtimeDir, StateDir: filepath.Join(t.TempDir(), "state")}.Defaults()
	lock, err := localstate.Lock(filepath.Join(runtimeDir, "broker.lock"))
	require.NoError(t, err)
	started := make(chan struct{})
	done := make(chan error, 1)
	expected := errors.New("test launcher reached")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() { done <- ensure(ctx, o, func(Options) error { close(started); return expected }) }()
	select {
	case <-started:
		t.Fatal("launched while previous broker still owned runtime")
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, lock.Close())
	require.ErrorIs(t, <-done, expected)
}

func TestBoundedRequestsCannotCreateUndeliverableHistory(t *testing.T) {
	o, calls := brokerFixture(t)
	alias := strings.Repeat("c", 128<<10)
	configText, err := os.ReadFile(o.Kubeconfig) // #nosec G304 -- Test-owned temporary kubeconfig.
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(o.Kubeconfig, []byte(strings.ReplaceAll(string(configText), "explicit", alias)), 0o600)) //nolint:gosec // Test-owned kubeconfig path from brokerFixture.
	client := NewClient(o)
	view, err := client.Start(t.Context(), api.StartRequest{Session: "qa", Key: "start", Context: alias})
	require.NoError(t, err)
	_, err = client.Submit(t.Context(), api.SubmitRequest{
		Session: "qa", Key: "large-target", Investigation: view.Investigation.ID,
		Alternatives: []inv.Alternative{{
			Target:   inv.Target{Cluster: view.Investigation.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "qa", Name: strings.Repeat("n", 150<<10), UID: "uid"},
			Observed: inv.Observed{ResourceVersion: "1", Time: time.Now()}, Action: inv.Action{Kind: "restart"},
		}},
	})
	require.ErrorContains(t, err, "invalid_argument")
	retained, err := client.Get(t.Context(), api.GetRequest{Investigation: view.Investigation.ID})
	require.NoError(t, err)
	require.Empty(t, retained.Investigation.Proposals)
	_, err = client.Start(t.Context(), api.StartRequest{Session: "qa", Key: "later", Context: alias})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		status, e := BrokerStatus(t.Context(), o)
		return e == nil && status.Undelivered == 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Zero(t, calls.Load())
}

func TestCancelledStartupLockWaitDoesNotLaunch(t *testing.T) {
	for _, name := range []string{"startup.lock", "broker.lock"} {
		t.Run(name, func(t *testing.T) {
			o, calls := brokerFixture(t)
			lock, err := localstate.Lock(filepath.Join(o.RuntimeDir, name))
			require.NoError(t, err)
			defer func() { _ = lock.Close() }()
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			launched := false
			err = ensure(ctx, o.Defaults(), func(Options) error { launched = true; return nil })
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.False(t, launched)
			require.Zero(t, calls.Load())
			_, err = os.Lstat(Socket(o))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestOversizedClientRequestRejectedBeforeStartup(t *testing.T) {
	o, calls := brokerFixture(t)
	_, err := NewClient(o).Start(t.Context(), api.StartRequest{Session: "qa", Key: "large", Context: "explicit", Summary: strings.Repeat("x", 300<<10)})
	var apiErr *api.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "invalid_argument", apiErr.Code)
	_, err = os.Lstat(Socket(o))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Zero(t, calls.Load())
}

func TestSlowRequestWithinClientDeadlineReturnsCommittedResult(t *testing.T) {
	o, _ := brokerFixture(t)
	var once sync.Once
	o.Resolve = func(string) (string, error) {
		once.Do(func() { time.Sleep(11 * time.Second) })
		return "test-cluster", nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, o) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	require.Eventually(t, func() bool { _, err := BrokerStatus(t.Context(), o); return err == nil }, 3*time.Second, 20*time.Millisecond)
	client := NewClient(o)
	p := api.StartRequest{Session: "qa", Key: "slow", Context: "explicit", Summary: "slow read-only resolution"}
	view, err := client.Start(t.Context(), p)
	require.NoError(t, err)
	require.NotEmpty(t, view.Investigation.ID)
	retried, err := client.Start(t.Context(), p)
	require.NoError(t, err)
	require.Equal(t, view.Investigation.ID, retried.Investigation.ID)
}
