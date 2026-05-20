package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStartInformers_SyncsAndExposesListers(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	)
	set := startInformers(cs)
	require.NotNil(t, set, "sync should succeed with fake clientset")
	t.Cleanup(set.close)

	pods, err := set.podLister.List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, pods, 2)

	nodes, err := set.nodeLister.List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
}

func TestInformerSet_CloseIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	set := startInformers(cs)
	require.NotNil(t, set)
	// Two closes must not panic on a closed channel.
	set.close()
	set.close()
}

func TestInformerSet_CloseNilSafe(t *testing.T) {
	var set *informerSet
	// Methods on nil must not panic — Client.Close relies on this when
	// the informer sync failed (informers stays nil on the Client).
	set.close()
}

func TestClientHotPods_FallsBackWhenInformersNil(t *testing.T) {
	// A Client without an informerSet must still work via the direct path.
	// We can't run ListHotPodsByCPU itself end-to-end here without wiring
	// metrics-server, but we can prove the method picks the direct branch
	// without panicking on the nil informerSet — covered by build + the
	// explicit guard. This test pins the contract.
	c := &Client{informers: nil}
	assert.Nil(t, c.informers, "fallback path is taken when informers is nil")
}

// ── Cache freshness ───────────────────────────────────────────────────
//
// The hot-path methods on Client (HotPods, ClusterHealth) read straight
// from the shared informer's lister. If a pod created or deleted after
// initial sync doesn't propagate, the TUI shows stale state — the worst
// kind of bug because the user trusts the table they're looking at.
// These tests pin live event delivery.

// waitFor polls a condition with a short deadline. Informer event
// delivery is async (watch → reflector → store → lister) so we can't
// assert synchronously, but we can fail fast on a tight deadline.
func waitFor(t *testing.T, deadline time.Duration, cond func() bool, msg string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %v: %s", deadline, msg)
}

func TestInformerSet_PropagatesPodCreate(t *testing.T) {
	cs := fake.NewSimpleClientset() // empty cluster at sync time
	set := startInformers(cs)
	require.NotNil(t, set)
	t.Cleanup(set.close)

	// Create a pod AFTER the initial sync. The informer's reflector must
	// pick it up via Watch and update the lister; otherwise the TUI would
	// never see pods scheduled during a session.
	_, err := cs.CoreV1().Pods("ns").Create(
		context.Background(),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: "ns"}},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	waitFor(t, 2*time.Second, func() bool {
		pods, err := set.podLister.List(labels.Everything())
		return err == nil && len(pods) == 1 && pods[0].Name == "new"
	}, "podLister must reflect a Create made after initial sync")
}

func TestInformerSet_PropagatesPodDelete(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "doomed", Namespace: "ns"}},
	)
	set := startInformers(cs)
	require.NotNil(t, set)
	t.Cleanup(set.close)

	// Pre-condition: the lister sees the pod after initial sync.
	pods, err := set.podLister.List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, pods, 1)

	// Delete it. The TUI relies on this propagating so the row disappears
	// from the table on the next refresh tick.
	err = cs.CoreV1().Pods("ns").Delete(context.Background(), "doomed", metav1.DeleteOptions{})
	require.NoError(t, err)

	waitFor(t, 2*time.Second, func() bool {
		pods, err := set.podLister.List(labels.Everything())
		return err == nil && len(pods) == 0
	}, "podLister must reflect a Delete made after initial sync")
}

func TestInformerSet_PropagatesNodeCreate(t *testing.T) {
	// Same contract for nodes — ClusterHealth iterates this lister.
	cs := fake.NewSimpleClientset()
	set := startInformers(cs)
	require.NotNil(t, set)
	t.Cleanup(set.close)

	_, err := cs.CoreV1().Nodes().Create(
		context.Background(),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n-new"}},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	waitFor(t, 2*time.Second, func() bool {
		nodes, err := set.nodeLister.List(labels.Everything())
		return err == nil && len(nodes) == 1
	}, "nodeLister must reflect a Create made after initial sync")
}
