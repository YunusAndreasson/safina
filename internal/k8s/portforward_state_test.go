package k8s

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func intStrInt(v int32) intstr.IntOrString   { return intstr.FromInt32(v) }
func intStrName(s string) intstr.IntOrString { return intstr.FromString(s) }

// pinForward registers a fake PortForward in fm without spinning up the real
// SPDY tunnel. Used to exercise the bookkeeping (Stop / StopAll / List
// snapshot / sort) without the test depending on a live API server.
func pinForward(fm *ForwardManager, id string, started time.Time, ports int) *PortForward {
	pf := &PortForward{
		ID:          id,
		ContextName: "ctx",
		Namespace:   "ns",
		TargetKind:  PortForwardTargetPod,
		TargetName:  "p",
		LocalPort:   ports,
		RemotePort:  ports,
		StartedAt:   started,
		stop:        make(chan struct{}),
		ready:       make(chan struct{}),
		errCh:       make(chan error, 1),
	}
	fm.mu.Lock()
	fm.active[id] = pf
	fm.mu.Unlock()
	return pf
}

func TestForwardManager_Stop_RemovesFromActiveAndClosesStop(t *testing.T) {
	// Stop must remove the entry from active (so it doesn't show up in
	// List or Count again) AND close the stop chan (so the running
	// goroutine notices and tears the SPDY tunnel down). A bug missing
	// either half leaks a forward.
	fm := NewForwardManager()
	pf := pinForward(fm, "f1", time.Now(), 8080)
	require.Equal(t, 1, fm.Count())

	require.NoError(t, fm.Stop("f1"))
	assert.Equal(t, 0, fm.Count(), "Stop must remove from active map")

	select {
	case <-pf.stop:
		// good — stop channel closed
	case <-time.After(time.Second):
		t.Fatal("Stop must close the stop chan so the forwarder goroutine exits")
	}

	// Second Stop on the same id must error rather than panic on
	// already-closed channel.
	err := fm.Stop("f1")
	require.Error(t, err)
}

func TestForwardManager_StopAll_TearsDownEveryForward(t *testing.T) {
	fm := NewForwardManager()
	pf1 := pinForward(fm, "f1", time.Now(), 8080)
	pf2 := pinForward(fm, "f2", time.Now(), 8081)
	pf3 := pinForward(fm, "f3", time.Now(), 8082)

	fm.StopAll()
	assert.Equal(t, 0, fm.Count(), "StopAll must drain the active map")

	for _, pf := range []*PortForward{pf1, pf2, pf3} {
		select {
		case <-pf.stop:
		case <-time.After(time.Second):
			t.Fatalf("forward %s: stop chan must close after StopAll", pf.ID)
		}
	}
}

func TestForwardManager_StopAll_IdempotentWhenEmpty(t *testing.T) {
	// App-exit code calls StopAll regardless of whether forwards were ever
	// started — must not panic.
	fm := NewForwardManager()
	require.NotPanics(t, func() { fm.StopAll() })
}

func TestForwardManager_List_SortsByStartTimeOldestFirst(t *testing.T) {
	// Operator's mental model: forwards listed in the order they opened
	// them. The topbar pill count is also stable because we hand back a
	// sorted slice.
	fm := NewForwardManager()
	now := time.Now()
	pinForward(fm, "newest", now.Add(2*time.Minute), 9001)
	pinForward(fm, "oldest", now, 9000)
	pinForward(fm, "middle", now.Add(1*time.Minute), 9002)

	got := fm.List()
	require.Len(t, got, 3)

	// Walk the result list — must be ascending by StartedAt.
	for i := 1; i < len(got); i++ {
		assert.False(t, got[i].StartedAt.Before(got[i-1].StartedAt),
			"List must be sorted oldest-first: %d (%v) came before %d (%v)",
			i, got[i].StartedAt, i-1, got[i-1].StartedAt)
	}
	assert.Equal(t, "oldest", got[0].ID)
	assert.Equal(t, "newest", got[len(got)-1].ID)
}

func TestForwardManager_List_ReportsStaleWhenForwardErrored(t *testing.T) {
	// If the forwarder pushed an error onto errCh (pod died, SPDY hung up),
	// List must surface stale=true so the modal renders the row dimmed
	// with a "stop" hint, not as a healthy forward.
	fm := NewForwardManager()
	pf := pinForward(fm, "f1", time.Now(), 8080)
	// Pre-load an error.
	pf.errCh <- assertError("pod went away")

	got := fm.List()
	require.Len(t, got, 1)
	assert.True(t, got[0].Stale, "List must report stale=true when errCh holds an error")

	// Critical invariant: List drains errCh but re-buffers it so a SECOND
	// List call still sees the error. A bug that drained-without-rebuffer
	// would make staleness flicker between visible and not.
	got2 := fm.List()
	require.Len(t, got2, 1)
	assert.True(t, got2[0].Stale, "stale signal must survive a re-List")
}

func TestForwardManager_Count_MatchesActiveAfterMixedOps(t *testing.T) {
	fm := NewForwardManager()
	pinForward(fm, "a", time.Now(), 1)
	pinForward(fm, "b", time.Now(), 2)
	pinForward(fm, "c", time.Now(), 3)
	require.Equal(t, 3, fm.Count())

	require.NoError(t, fm.Stop("b"))
	require.Equal(t, 2, fm.Count())

	// List length must match Count exactly (no leak).
	assert.Len(t, fm.List(), fm.Count())

	ids := []string{}
	for _, s := range fm.List() {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, []string{"a", "c"}, ids)
}

// ── resolvePodForService ──────────────────────────────────────────────

func TestResolvePodForService_PicksFirstRunningPod(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "shop"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "checkout"},
			Ports: []corev1.ServicePort{
				{Name: "http", TargetPort: intStrInt(8080)},
			},
		},
	}
	p1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "checkout-0",
			Namespace: "shop",
			Labels:    map[string]string{"app": "checkout"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	p2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "checkout-1",
			Namespace: "shop",
			Labels:    map[string]string{"app": "checkout"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := fake.NewSimpleClientset(svc, p1, p2)

	pod, ports, err := resolvePodForService(context.Background(), cs, "shop", "checkout")
	require.NoError(t, err)
	assert.Contains(t, []string{"checkout-0", "checkout-1"}, pod, "must pick one of the matching running pods")
	assert.Equal(t, []int32{8080}, ports, "int-typed targetPort must be returned for prefill")
}

func TestResolvePodForService_SkipsTerminatingPods(t *testing.T) {
	// A pod with DeletionTimestamp set is going away. Forwarding to it
	// would surface a dead connection. Resolver must prefer a healthy
	// replica.
	now := metav1.NewTime(time.Now())
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "x"},
		},
	}
	dying := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dying", Namespace: "ns",
			Labels:            map[string]string{"app": "x"},
			DeletionTimestamp: &now,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	healthy := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "healthy", Namespace: "ns",
			Labels: map[string]string{"app": "x"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := fake.NewSimpleClientset(svc, dying, healthy)
	pod, _, err := resolvePodForService(context.Background(), cs, "ns", "svc")
	require.NoError(t, err)
	assert.Equal(t, "healthy", pod, "must skip pods with DeletionTimestamp set")
}

func TestResolvePodForService_NoSelectorReturnsActionableError(t *testing.T) {
	// Headless / ExternalName services have no selector. Forwarding via
	// the SPDY pod path can't work; surface a clear error instead of
	// crashing later in StartPortForward.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "headless", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{}, // no Selector
	}
	cs := fake.NewSimpleClientset(svc)
	_, _, err := resolvePodForService(context.Background(), cs, "ns", "headless")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no selector", "error must explain the service's shape")
}

func TestResolvePodForService_NoRunningPodReturnsActionableError(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "x"}},
	}
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pending", Namespace: "ns",
			Labels: map[string]string{"app": "x"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	cs := fake.NewSimpleClientset(svc, pending)
	_, _, err := resolvePodForService(context.Background(), cs, "ns", "svc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no running pod")
}

func TestResolvePodForService_DropsNamedTargetPorts(t *testing.T) {
	// A service with a NAMED targetPort can't be resolved to an integer
	// without per-pod inspection. The prefill hint code drops names so
	// the modal doesn't suggest a port the SPDY forwarder will reject.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "ns"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "x"},
			Ports: []corev1.ServicePort{
				{Name: "http", TargetPort: intStrName("web")},     // named — drop
				{Name: "metrics", TargetPort: intStrInt(9090)},    // int — keep
				{Name: "grpc", TargetPort: intStrInt(7070)},       // int — keep
				{Name: "internal", TargetPort: intStrName("rpc")}, // named — drop
			},
		},
	}
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "ns",
			Labels: map[string]string{"app": "x"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	cs := fake.NewSimpleClientset(svc, p)
	_, ports, err := resolvePodForService(context.Background(), cs, "ns", "svc")
	require.NoError(t, err)
	assert.ElementsMatch(t, []int32{9090, 7070}, ports, "only int targetPorts must be reported")
}

// ── small testing helpers ─────────────────────────────────────────────

func assertError(s string) error { return &simpleErr{s: s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
