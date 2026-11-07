package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/yunus/safina/internal/history"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

// CordonNode marks the node unschedulable. New pods won't be placed here,
// but existing pods stay put — Drain is the eviction step.
//
// We use a strategic-merge patch on .spec.unschedulable rather than a full
// Update so we don't fight any controller mutating the node spec concurrently
// (cluster-autoscaler, in-tree provisioners). This is the same approach
// kubectl cordon takes.
func CordonNode(ctx context.Context, cs kubernetes.Interface, name string) error {
	return setUnschedulable(ctx, cs, name, true)
}

// UncordonNode is the inverse: clears .spec.unschedulable so the scheduler
// can place pods on this node again.
func UncordonNode(ctx context.Context, cs kubernetes.Interface, name string) error {
	return setUnschedulable(ctx, cs, name, false)
}

func setUnschedulable(ctx context.Context, cs kubernetes.Interface, name string, unschedulable bool) error {
	patch := struct {
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
	}{}
	patch.Spec.Unschedulable = unschedulable
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	var op *history.Operation
	if parent := history.FromContext(ctx); parent != nil {
		action := "cordon"
		if !unschedulable {
			action = "uncordon"
		}
		op = parent.Store.Begin(parent.Target, action, map[string]string{"drainOperation": parent.ID})
	}
	_, err = cs.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, body, metav1.PatchOptions{})
	if op != nil {
		op.Finish(err, "accepted")
	}
	return err
}

// DrainOptions tunes a drain. The TUI passes a hardcoded set of opinions
// (ignore DaemonSets, delete emptyDir-backed pods, 30s grace, 10m timeout)
// because the on-caller never wants the modal-with-checkboxes k9s offers —
// any choice they'd make is either dangerous or the default.
type DrainOptions struct {
	IgnoreDaemonSets bool
	DeleteEmptyDir   bool
	Force            bool // permit unmanaged pods; mirror pods are always skipped
	GracePeriod      time.Duration
	Timeout          time.Duration
}

// DefaultDrainOptions is the only option set the TUI ever uses. Power
// users who need something else still have `kubectl drain` outside safina.
func DefaultDrainOptions() DrainOptions {
	return DrainOptions{
		IgnoreDaemonSets: true,
		DeleteEmptyDir:   true,
		Force:            false,
		GracePeriod:      30 * time.Second,
		Timeout:          10 * time.Minute,
	}
}

// DrainEvent is one tick of progress emitted to the caller's channel as a
// drain runs. The progress modal in the TUI consumes these — one tick when
// cordoning begins, one per eviction round, a final Done=true at the end
// (or when an error short-circuits the loop).
type DrainEvent struct {
	Phase          string // "cordoning" | "evicting" | "waiting" | "done"
	PodsTotal      int
	PodsEvicted    int
	PodsRemaining  int
	RemainingPods  []string // human-readable "ns/name" for the modal hint
	Msg            string
	Err            error
	Done           bool
	StartTimestamp time.Time
}

// DrainNode delegates filtering, eviction retries and termination waiting to kubectl.
// Safina retains progress reporting and per-attempt activity recording.
func DrainNode(ctx context.Context, cs kubernetes.Interface, nodeName string, opts DrainOptions, progress chan<- DrainEvent) error {
	defer close(progress)
	start := time.Now()
	emit := func(e DrainEvent) {
		e.StartTimestamp = start
		select {
		case progress <- e:
		case <-ctx.Done():
		}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	drainCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	helper := newDrainHelper(drainCtx, cs, opts)
	list, errs := helper.GetPodsForDeletion(nodeName)
	if err := errors.Join(errs...); err != nil {
		emit(DrainEvent{Phase: "done", Done: true, Err: err})
		return err
	}
	pods := list.Pods()
	// Validate eviction support before cordoning. The wrapped discovery client also
	// checks each later discovery, preventing kubectl's direct-delete fallback.
	if len(pods) > 0 {
		if _, err := drain.CheckEvictionSupport(helper.Client); err != nil {
			emit(DrainEvent{Phase: "done", Done: true, Err: err})
			return err
		}
	}
	emit(DrainEvent{Phase: "cordoning", Msg: "cordoning " + nodeName})
	if err := CordonNode(drainCtx, cs, nodeName); err != nil {
		emit(DrainEvent{Phase: "done", Done: true, Err: err})
		return fmt.Errorf("cordon: %w", err)
	}
	// Keep a cancelled drain cordoned: some evictions may already be accepted.
	// Uncordoning is an explicit operator action, including for pre-cordoned nodes.
	// List again after cordoning to include pods scheduled during preflight.
	list, errs = helper.GetPodsForDeletion(nodeName)
	if err := errors.Join(errs...); err != nil {
		emit(DrainEvent{Phase: "done", Done: true, Err: err})
		return err
	}
	pods = list.Pods()
	uids := make(map[string]types.UID, len(pods))
	for _, pod := range pods {
		uids[pod.Namespace+"/"+pod.Name] = pod.UID
	}
	helper.Client = drainClient{Interface: cs, uids: uids}
	var mu sync.Mutex
	remaining := make(map[string]bool, len(pods))
	for _, p := range pods {
		remaining[p.Namespace+"/"+p.Name] = true
	}
	publish := func(phase string) {
		names := make([]string, 0, len(remaining))
		for name := range remaining {
			names = append(names, name)
		}
		emit(DrainEvent{Phase: phase, PodsTotal: len(pods), PodsEvicted: len(pods) - len(remaining), PodsRemaining: len(remaining), RemainingPods: names})
	}
	publish("evicting")
	helper.OnPodDeletionOrEvictionFinished = func(p *corev1.Pod, _ bool, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			delete(remaining, p.Namespace+"/"+p.Name)
		}
		publish("waiting")
	}
	err := helper.DeleteOrEvictPods(pods)
	if err != nil && drainCtx.Err() != nil {
		// kubectl's timeout errors do not consistently wrap the context error.
		// Preserve it so the logbook distinguishes cancellation and timeout.
		err = errors.Join(err, drainCtx.Err())
	}
	message := "drained " + nodeName
	if err != nil {
		message = "drain incomplete; node remains cordoned"
	} else {
		// An eviction can return NotFound before kubectl's completion callback.
		clear(remaining)
	}
	emit(DrainEvent{Phase: "done", Done: true, Err: err, Msg: message, PodsTotal: len(pods), PodsEvicted: len(pods) - len(remaining), PodsRemaining: len(remaining)})
	return err
}

func newDrainHelper(ctx context.Context, cs kubernetes.Interface, opts DrainOptions) *drain.Helper {
	return &drain.Helper{
		Ctx: ctx, Client: drainClient{Interface: cs}, Force: opts.Force,
		GracePeriodSeconds:  int(opts.GracePeriod / time.Second),
		IgnoreAllDaemonSets: opts.IgnoreDaemonSets, DeleteEmptyDirData: opts.DeleteEmptyDir,
		Timeout: opts.Timeout, EvictErrorRetryDelay: 2 * time.Second, ChunkSize: 500,
		Out: io.Discard, ErrOut: io.Discard,
	}
}

// listDrainablePods includes terminating pods until they disappear and returns
// errors for unmanaged pods and disallowed local storage rather than hiding them.
func listDrainablePods(ctx context.Context, cs kubernetes.Interface, nodeName string, opts DrainOptions) ([]corev1.Pod, error) {
	helper := newDrainHelper(ctx, cs, opts)
	list, errs := helper.GetPodsForDeletion(nodeName)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return list.Pods(), nil
}

// PodsOnNode counts the pods scheduled on nodeName — used by the drain
// confirm modal to show the operator how many evictions they're triggering
// before they say yes. Mirrors listDrainablePods's filter set so the
// number reflects what will actually move.
func PodsOnNode(ctx context.Context, cs kubernetes.Interface, nodeName string, opts DrainOptions) (int, error) {
	pods, err := listDrainablePods(ctx, cs, nodeName, opts)
	if err != nil {
		return 0, err
	}
	return len(pods), nil
}
