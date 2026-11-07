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
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yunus/safina/internal/broker/api"
	inv "github.com/yunus/safina/internal/investigation"
	"github.com/yunus/safina/internal/localstate"
	"golang.org/x/sys/unix"
)

// Client reconnects on each call and starts a broker if necessary. An uncertain
// call is returned to the caller; retry using the SAME idempotency key.
type Client struct{ options Options }

var _ api.InvestigationClient = (*Client)(nil)

func NewClient(options Options) *Client { return &Client{options: options.Defaults()} }

func transport(options Options) *http.Client {
	return &http.Client{Timeout: requestTimeout, Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", Socket(options))
			if err != nil {
				return nil, err
			}
			if err = sameUser(conn); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}}
}

func request(ctx context.Context, options Options, method, path string, body []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://safina"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	client := transport(options)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("broker HTTP status %d", resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	return decode(data, result)
}

// BrokerStatus and Stop do not auto-start a broker.
func BrokerStatus(ctx context.Context, options Options) (Status, error) {
	var status Status
	err := request(ctx, options, http.MethodGet, "/v1/status", nil, &status)
	if err == nil && status.Version != api.Version {
		err = errors.New("incompatible broker protocol")
	}
	return status, err
}

func Stop(ctx context.Context, options Options) error {
	if _, err := BrokerStatus(ctx, options); err != nil {
		return err
	}
	return request(ctx, options, http.MethodPost, "/v1/stop", nil, nil)
}

func compatible(status Status, o Options) error {
	if status.StateDir != o.StateDir || status.Kubeconfig != o.Kubeconfig {
		return errors.New("broker configuration differs; stop the existing broker explicitly first")
	}
	return nil
}

// Ensure serializes starters independently of the long-lived broker lock. A
// readiness response, not PID existence, establishes successful startup.
func Ensure(ctx context.Context, options Options) error {
	return ensure(ctx, options.Defaults(), launch)
}

func ensure(ctx context.Context, o Options, start func(Options) error) error {
	if status, err := BrokerStatus(ctx, o); err == nil {
		return compatible(status, o)
	} else if !absent(err) {
		return err
	}
	if err := localstate.Directory(o.RuntimeDir); err != nil {
		return err
	}
	var lock *os.File
	for {
		var err error
		lock, err = localstate.Lock(filepath.Join(o.RuntimeDir, "startup.lock"))
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if err = pause(ctx); err != nil {
			return err
		}
	}
	defer func() { _ = lock.Close() }()
	if status, err := BrokerStatus(ctx, o); err == nil {
		return compatible(status, o)
	} else if !absent(err) {
		return err
	}
	// Socket closure precedes final journal/history cleanup. Wait for runtime
	// ownership to be released before starting the replacement process.
	for {
		runtimeLock, lockErr := localstate.Lock(filepath.Join(o.RuntimeDir, "broker.lock"))
		if lockErr == nil {
			if closeErr := runtimeLock.Close(); closeErr != nil {
				return closeErr
			}
			break
		}
		if !errors.Is(lockErr, unix.EWOULDBLOCK) && !errors.Is(lockErr, unix.EAGAIN) {
			return lockErr
		}
		if status, statusErr := BrokerStatus(ctx, o); statusErr == nil {
			return compatible(status, o)
		}
		if pauseErr := pause(ctx); pauseErr != nil {
			return pauseErr
		}
	}
	if err := start(o); err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		status, err := BrokerStatus(readyCtx, o)
		if err == nil {
			return compatible(status, o)
		}
		if !absent(err) {
			return err
		}
		if err = pause(readyCtx); err != nil {
			return fmt.Errorf("broker did not become ready; inspect broker.log: %w", err)
		}
	}
}

func absent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func pause(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func launch(o Options) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := localstate.Open(filepath.Join(o.RuntimeDir, "broker.log"), unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	args := []string{"serve", "--runtime-dir", o.RuntimeDir, "--state-dir", o.StateDir, "--kubeconfig", o.Kubeconfig}
	if o.History.Directory != "" {
		args = append(args, "--history-dir", o.History.Directory)
	}
	// Deliberately independent of the client's cancellation and process group.
	//nolint:noctx // The detached broker must outlive the initiating request and process.
	child := exec.Command(executable, args...) // #nosec G204 -- Current executable, fixed subcommand and structured local options.
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	child.Stdout = log
	child.Stderr = log
	if err = child.Start(); err != nil {
		return err
	}
	go func() { _ = child.Wait() }()
	return nil
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	return c.callPath(ctx, "/v1/investigation", method, params, result)
}

func (c *Client) callPath(ctx context.Context, path, method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req := api.Request{Version: api.Version, ID: inv.ID(), Method: method, Params: p}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if len(b) > requestBytes {
		return &api.Error{Code: "invalid_argument", Message: "request exceeds 256 KiB limit"}
	}
	if err = Ensure(ctx, c.options); err != nil {
		return err
	}
	var response api.Response
	if err = request(ctx, c.options, http.MethodPost, path, b, &response); err != nil {
		return err
	}
	if response.Version != api.Version || response.ID != req.ID {
		return errors.New("invalid broker response")
	}
	if response.Error != nil {
		return response.Error
	}
	return decode(response.Result, result)
}

func (c *Client) Start(ctx context.Context, p api.StartRequest) (api.View, error) {
	var result api.View
	err := c.call(ctx, "start_investigation", p, &result)
	return result, err
}

func (c *Client) Submit(ctx context.Context, p api.SubmitRequest) (api.Submitted, error) {
	var result api.Submitted
	err := c.call(ctx, "submit_proposals", p, &result)
	return result, err
}

func (c *Client) Get(ctx context.Context, p api.GetRequest) (api.View, error) {
	var result api.View
	err := c.call(ctx, "get_investigation", p, &result)
	return result, err
}

func (c *Client) Contexts(ctx context.Context, p api.Empty) (api.Contexts, error) {
	var out api.Contexts
	err := c.call(ctx, "list_contexts", p, &out)
	return out, err
}

func (c *Client) Inspect(ctx context.Context, p api.InspectRequest) (api.Observation, error) {
	var out api.Observation
	err := c.call(ctx, "inspect_workload", p, &out)
	return out, err
}

func (c *Client) Events(ctx context.Context, p api.EventsRequest) (api.Observation, error) {
	var out api.Observation
	err := c.call(ctx, "read_events", p, &out)
	return out, err
}

func (c *Client) Logs(ctx context.Context, p api.LogsRequest) (api.Observation, error) {
	var out api.Observation
	err := c.call(ctx, "read_logs", p, &out)
	return out, err
}

func (c *Client) History(ctx context.Context, p api.HistoryRequest) (api.HistoryResult, error) {
	var out api.HistoryResult
	err := c.call(ctx, "query_history", p, &out)
	return out, err
}

func (c *Client) Evidence(ctx context.Context, p api.EvidenceRequest) (api.Observation, error) {
	var out api.Observation
	err := c.call(ctx, "get_evidence", p, &out)
	return out, err
}
