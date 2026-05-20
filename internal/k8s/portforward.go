package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForwardTargetKind names the two things you can forward to.
const (
	PortForwardTargetPod     = "Pod"
	PortForwardTargetService = "Service"
)

// PortForward tracks one active local→cluster forward. It's the row-level
// record both the topbar pill and the forwards-list modal read from.
//
// Why we pin the resolved pod (even for Service targets): client-go's
// portforward.New only speaks to a Pod, so a Service forward picks one
// ready endpoint and rides it. If that pod dies, the forward stays bound
// to it (showing as `stale` rather than silently re-targeting) — silent
// re-resolution would surprise the operator the first time they noticed
// a different pod handling their curl.
type PortForward struct {
	ID            string
	ContextName   string
	Namespace     string
	TargetKind    string // PortForwardTargetPod / PortForwardTargetService
	TargetName    string // the Pod or Service name the user picked
	ResolvedPod   string // the pod the forwarder is actually bound to
	LocalPort     int
	RemotePort    int
	StartedAt     time.Time
	requestedPort int // what the user typed; LocalPort may differ if we picked a free one
	stop          chan struct{}
	ready         chan struct{}
	errCh         chan error
}

// PortForwardSnapshot is the value-typed view returned by ListPortForwards.
// We never hand out *PortForward externally — readers race the eviction
// goroutine on every field, and a snapshot is cheap.
type PortForwardSnapshot struct {
	ID          string
	ContextName string
	Namespace   string
	TargetKind  string
	TargetName  string
	ResolvedPod string
	LocalPort   int
	RemotePort  int
	StartedAt   time.Time
	// Stale reports that the forward was started but is no longer healthy
	// (the pod went away). The list modal flags these so the operator can
	// kill+restart rather than wonder why curl times out.
	Stale bool
}

// ForwardManager owns every active port-forward in this safina process.
// It lives on k8s.Manager (one instance per process) so forwards survive
// every modal/state transition — including drilling into a different
// context. Shut down via Close() on app exit.
type ForwardManager struct {
	mu     sync.Mutex
	active map[string]*PortForward
	seq    int
}

// NewForwardManager builds an empty manager. The Manager owns one.
func NewForwardManager() *ForwardManager {
	return &ForwardManager{active: make(map[string]*PortForward)}
}

// PickFreeLocalPort asks the kernel for an ephemeral port we can bind. Used
// when the user-requested port is already in use: rather than make them
// retry, we transparently grab a free one and banner the substitution.
func PickFreeLocalPort() (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// localPortInUse is a non-binding probe: try to listen, close immediately if
// we got it. The Listen + Close dance briefly holds the port (under a ms);
// the portforward goroutine that runs right after will get the same port
// back from the kernel because we only released it. Tiny race window, but
// the alternative is parsing /proc/net/tcp which is wildly OS-specific.
func localPortInUse(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

// resolvePodForService finds one ready pod backing svcName. We pick the
// first running pod that matches the selector — fancier ranking (least
// loaded, oldest, etc.) isn't worth the code at this scale.
func resolvePodForService(ctx context.Context, cs kubernetes.Interface, namespace, svcName string) (pod string, remotePorts []int32, err error) {
	svc, err := cs.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("get service: %w", err)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", nil, fmt.Errorf("service %q has no selector (likely headless or externalName)", svcName)
	}
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return "", nil, fmt.Errorf("list pods: %w", err)
	}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			pod = p.Name
			break
		}
	}
	if pod == "" {
		return "", nil, fmt.Errorf("no running pod for service %q", svcName)
	}
	for _, sp := range svc.Spec.Ports {
		// Service.targetPort may be a name or an int; only the int form is
		// useful for the SPDY forwarder. Named target ports get resolved per
		// pod which isn't necessary for the prefill hint — drop names.
		if sp.TargetPort.Type == 0 {
			remotePorts = append(remotePorts, sp.TargetPort.IntVal)
		}
	}
	return pod, remotePorts, nil
}

// StartPortForward opens a new forward. For Service targets it resolves one
// ready endpoint pod first and pins to it. Returns the running PortForward
// once the SPDY tunnel is ready (or an error if it never connects).
//
// Local-port collision: if the requested port is in use, we silently pick
// a free one. The returned PortForward carries the actual LocalPort the
// caller should advertise.
func (fm *ForwardManager) StartPortForward(
	ctx context.Context,
	cs kubernetes.Interface,
	restCfg *rest.Config,
	contextName, namespace, targetKind, targetName string,
	localPort, remotePort int,
) (*PortForward, error) {
	if targetKind != PortForwardTargetPod && targetKind != PortForwardTargetService {
		return nil, fmt.Errorf("unsupported target kind %q", targetKind)
	}
	if remotePort <= 0 || remotePort > 65535 {
		return nil, fmt.Errorf("remote port %d out of range", remotePort)
	}

	podName := targetName
	if targetKind == PortForwardTargetService {
		resolved, _, err := resolvePodForService(ctx, cs, namespace, targetName)
		if err != nil {
			return nil, err
		}
		podName = resolved
	}

	if localPort <= 0 || localPort > 65535 {
		p, err := PickFreeLocalPort()
		if err != nil {
			return nil, fmt.Errorf("pick local port: %w", err)
		}
		localPort = p
	} else if localPortInUse(localPort) {
		p, err := PickFreeLocalPort()
		if err != nil {
			return nil, fmt.Errorf("pick local port: %w", err)
		}
		localPort = p
	}

	transport, upgrader, err := spdy.RoundTripperFor(restCfg)
	if err != nil {
		return nil, fmt.Errorf("spdy transport: %w", err)
	}

	// Build the portforward URL: POST /api/v1/namespaces/<ns>/pods/<pod>/portforward
	base, err := url.Parse(restCfg.Host)
	if err != nil {
		return nil, fmt.Errorf("parse host: %w", err)
	}
	base.Path = path.Join(base.Path, "api", "v1", "namespaces", namespace, "pods", podName, "portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, base)

	stop := make(chan struct{})
	ready := make(chan struct{})
	errCh := make(chan error, 1)

	fwd, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stop, ready,
		io.Discard, io.Discard,
	)
	if err != nil {
		return nil, fmt.Errorf("new forwarder: %w", err)
	}

	go func() {
		errCh <- fwd.ForwardPorts()
	}()

	// Wait for ready (or an early error / context cancel). Without this the
	// caller can't trust LocalPort yet — the kernel hasn't bound it.
	select {
	case <-ready:
	case err := <-errCh:
		close(stop)
		return nil, fmt.Errorf("forward did not become ready: %w", err)
	case <-ctx.Done():
		close(stop)
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		close(stop)
		return nil, errors.New("forward did not become ready within 10s")
	}

	fm.mu.Lock()
	fm.seq++
	id := fmt.Sprintf("pf-%d", fm.seq)
	pf := &PortForward{
		ID:            id,
		ContextName:   contextName,
		Namespace:     namespace,
		TargetKind:    targetKind,
		TargetName:    targetName,
		ResolvedPod:   podName,
		LocalPort:     localPort,
		RemotePort:    remotePort,
		StartedAt:     time.Now(),
		requestedPort: localPort,
		stop:          stop,
		ready:         ready,
		errCh:         errCh,
	}
	fm.active[id] = pf
	fm.mu.Unlock()
	return pf, nil
}

// Stop tears down a forward. Idempotent; double-Stop is a no-op rather than
// a panic on the already-closed channel.
func (fm *ForwardManager) Stop(id string) error {
	fm.mu.Lock()
	pf, ok := fm.active[id]
	if !ok {
		fm.mu.Unlock()
		return fmt.Errorf("forward %q not found", id)
	}
	delete(fm.active, id)
	fm.mu.Unlock()
	close(pf.stop)
	return nil
}

// StopAll shuts every active forward down. Called on app exit.
func (fm *ForwardManager) StopAll() {
	fm.mu.Lock()
	ids := make([]string, 0, len(fm.active))
	for id := range fm.active {
		ids = append(ids, id)
	}
	fm.mu.Unlock()
	for _, id := range ids {
		_ = fm.Stop(id)
	}
}

// List returns a value-typed snapshot of every active forward. The slice is
// sorted by start time so the topbar pill's count is stable and the modal
// renders forwards in the order they were created.
func (fm *ForwardManager) List() []PortForwardSnapshot {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	out := make([]PortForwardSnapshot, 0, len(fm.active))
	for _, pf := range fm.active {
		stale := false
		select {
		case err := <-pf.errCh:
			// Drain — record the error as staleness but keep the entry in
			// the list so the user can see the row + stop it. Re-buffer the
			// signal so a follow-up List call still sees it.
			stale = err != nil
			pf.errCh <- err
		default:
		}
		out = append(out, PortForwardSnapshot{
			ID:          pf.ID,
			ContextName: pf.ContextName,
			Namespace:   pf.Namespace,
			TargetKind:  pf.TargetKind,
			TargetName:  pf.TargetName,
			ResolvedPod: pf.ResolvedPod,
			LocalPort:   pf.LocalPort,
			RemotePort:  pf.RemotePort,
			StartedAt:   pf.StartedAt,
			Stale:       stale,
		})
	}
	// Stable order: oldest first.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].StartedAt.Before(out[i].StartedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Count is a lock-cheap accessor for the topbar pill.
func (fm *ForwardManager) Count() int {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	return len(fm.active)
}

// ParsePortPair parses "L:R" or "R" (where L defaults to R). Returns an
// error on anything outside the [1,65535] range or with non-digit chunks.
func ParsePortPair(s string) (local, remote int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, errors.New("empty port pair")
	}
	parts := strings.SplitN(s, ":", 2)
	parseOne := func(p string) (int, error) {
		p = strings.TrimSpace(p)
		if p == "" {
			return 0, errors.New("empty port")
		}
		v := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("non-digit in port %q", p)
			}
			v = v*10 + int(r-'0')
			if v > 65535 {
				return 0, fmt.Errorf("port %q out of range", p)
			}
		}
		if v < 1 {
			return 0, fmt.Errorf("port %q out of range", p)
		}
		return v, nil
	}
	if len(parts) == 1 {
		v, err := parseOne(parts[0])
		if err != nil {
			return 0, 0, err
		}
		return v, v, nil
	}
	l, err := parseOne(parts[0])
	if err != nil {
		return 0, 0, err
	}
	r, err := parseOne(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return l, r, nil
}
