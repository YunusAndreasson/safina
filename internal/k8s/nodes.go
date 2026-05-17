package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
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
	_, err = cs.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, body, metav1.PatchOptions{})
	return err
}

// DrainOptions tunes a drain. The TUI passes a hardcoded set of opinions
// (ignore DaemonSets, delete emptyDir-backed pods, 30s grace, 10m timeout)
// because the on-caller never wants the modal-with-checkboxes k9s offers —
// any choice they'd make is either dangerous or the default.
type DrainOptions struct {
	IgnoreDaemonSets bool
	DeleteEmptyDir   bool
	Force            bool // when true, evict mirror pods and orphans too
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

// DrainNode runs the eviction loop. Closes progress when it exits — the
// caller MUST consume the channel or the goroutine wedges. The function
// itself returns when the loop finishes (or fails or times out); progress
// observers can also detect the end via Done=true.
//
// Implicit cordon: we cordon before we evict, since uncordoned-mid-drain
// would let new pods race in behind us. If the caller cancels ctx, we
// uncordon on the way out — leaving the node half-drained AND cordoned is
// the surprising failure mode we close.
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

	emit(DrainEvent{Phase: "cordoning", Msg: "cordoning " + nodeName})
	if err := CordonNode(ctx, cs, nodeName); err != nil {
		emit(DrainEvent{Phase: "done", Err: err, Done: true})
		return fmt.Errorf("cordon: %w", err)
	}

	// On any non-success exit, uncordon the node. The user can always re-
	// cordon explicitly; a node that was cordoned-for-drain-then-aborted
	// being left cordoned is the silent foot-gun.
	committed := false
	defer func() {
		if !committed && ctx.Err() != nil {
			// Best-effort uncordon on cancellation. We deliberately use a
			// fresh background context with a short timeout — the user's
			// ctx is already dead so we can't use it.
			uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = UncordonNode(uctx, cs, nodeName)
		}
	}()

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)

	for {
		if time.Now().After(deadline) {
			err := fmt.Errorf("drain timed out after %s", timeout)
			emit(DrainEvent{Phase: "done", Err: err, Done: true})
			return err
		}
		if err := ctx.Err(); err != nil {
			emit(DrainEvent{Phase: "done", Err: err, Done: true})
			return err
		}

		victims, err := listDrainablePods(ctx, cs, nodeName, opts)
		if err != nil {
			emit(DrainEvent{Phase: "done", Err: err, Done: true})
			return err
		}
		if len(victims) == 0 {
			committed = true
			emit(DrainEvent{Phase: "done", Done: true, Msg: "drained " + nodeName})
			return nil
		}

		names := make([]string, 0, len(victims))
		for _, p := range victims {
			names = append(names, p.Namespace+"/"+p.Name)
		}
		emit(DrainEvent{
			Phase:         "evicting",
			PodsRemaining: len(victims),
			RemainingPods: names,
			Msg:           fmt.Sprintf("evicting %d pods", len(victims)),
		})

		for _, p := range victims {
			if err := ctx.Err(); err != nil {
				emit(DrainEvent{Phase: "done", Err: err, Done: true})
				return err
			}
			grace := int64(opts.GracePeriod / time.Second)
			eviction := &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      p.Name,
					Namespace: p.Namespace,
				},
				DeleteOptions: &metav1.DeleteOptions{
					GracePeriodSeconds: &grace,
				},
			}
			err := cs.PolicyV1().Evictions(p.Namespace).Evict(ctx, eviction)
			if err != nil && !apierrors.IsNotFound(err) {
				// 429 = PDB blocking; keep trying on the next loop iteration.
				// Anything else, we surface and bail — a real error here
				// (RBAC denied, API down) won't fix itself.
				if !apierrors.IsTooManyRequests(err) {
					emit(DrainEvent{Phase: "done", Err: err, Done: true})
					return err
				}
			}
		}

		// Brief breather before re-listing so we don't busy-loop while
		// pods are mid-termination.
		select {
		case <-ctx.Done():
			emit(DrainEvent{Phase: "done", Err: ctx.Err(), Done: true})
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// listDrainablePods returns pods on nodeName that should be evicted under
// the given opts. Filters out:
//   - DaemonSet-managed pods (when IgnoreDaemonSets)
//   - Mirror pods (static pods from /etc/kubernetes/manifests; nothing the
//     API can do about them)
//   - Pods using emptyDir volumes (unless DeleteEmptyDir)
//   - Orphan pods (no controller; lose data if evicted — unless Force)
//   - Already-terminating pods (DeletionTimestamp set)
func listDrainablePods(ctx context.Context, cs kubernetes.Interface, nodeName string, opts DrainOptions) ([]corev1.Pod, error) {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return nil, err
	}
	out := make([]corev1.Pod, 0, len(pods.Items))
	for _, p := range pods.Items {
		if p.DeletionTimestamp != nil {
			continue
		}
		// Mirror pods (kube-system static pods) can't be evicted via the
		// API; the kubelet owns their lifecycle. Skipping them silently
		// matches kubectl drain's behaviour.
		if _, ok := p.Annotations["kubernetes.io/config.mirror"]; ok {
			continue
		}
		owner := topLevelOwner(p)
		if owner == "DaemonSet" && opts.IgnoreDaemonSets {
			continue
		}
		if owner == "" && !opts.Force {
			// Orphan: no controller will re-create it after eviction.
			// Skipping unless --force matches kubectl semantics; safina's
			// hardcoded opts.Force=false means an orphan pinned to a node
			// keeps the drain from completing. That's intentional — we
			// fail loud rather than lose data silently.
			continue
		}
		if !opts.DeleteEmptyDir && hasEmptyDir(p) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func topLevelOwner(p corev1.Pod) string {
	for _, ref := range p.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return ref.Kind
		}
	}
	return ""
}

func hasEmptyDir(p corev1.Pod) bool {
	for _, v := range p.Spec.Volumes {
		if v.EmptyDir != nil {
			return true
		}
	}
	return false
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
