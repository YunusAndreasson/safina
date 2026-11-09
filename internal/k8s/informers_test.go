package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

// waitFor polls a condition with a short 2s deadline. Informer sync and
// event delivery are async (watch → reflector → store → lister) so we can't
// assert synchronously, but we can fail fast on a tight deadline.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	const deadline = 2 * time.Second
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %v: %s", deadline, msg)
}

func TestStartInformers_SyncsAndExposesListers(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "d1", Namespace: "ns"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "s1", Namespace: "ns"}},
	)
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	// Start is non-blocking; accessors return nil until each kind's initial
	// sync lands, then the lister serves.
	waitFor(t, func() bool { return set.Pods() != nil }, "pod informer must sync")
	pods, err := set.Pods().List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, pods, 2)

	waitFor(t, func() bool { return set.Nodes() != nil }, "node informer must sync")
	nodes, err := set.Nodes().List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, nodes, 1)

	waitFor(t, func() bool { return set.Deployments() != nil }, "deployment informer must sync")
	deps, err := set.Deployments().List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, deps, 1)

	waitFor(t, func() bool { return set.Services() != nil }, "service informer must sync")
	svcs, err := set.Services().List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, svcs, 1)
}

func TestInformerSet_CloseIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	set := startInformers(cs, "")
	require.NotNil(t, set)
	// Two closes must not panic on a closed channel.
	set.close()
	set.close()
}

func TestInformerSet_CloseNilSafe(t *testing.T) {
	var set *informerSet
	// Methods on nil must not panic — Client.Close relies on this when
	// informers were never started. Accessors share the contract: nil set
	// reads as "no cache, use the direct path".
	set.close()
	assert.Nil(t, set.Pods())
	assert.Nil(t, set.Secrets())
}

func TestClient_InformersNilUntilStarted(t *testing.T) {
	// A Client whose informers were never started must report nil all the
	// way down the accessor chain — this is the direct-path fallback contract
	// every cache-aware method leans on.
	c := &Client{}
	assert.Nil(t, c.Informers())
	assert.Nil(t, c.Informers().Pods())
	// And a nil *Client must be safe too (Manager map misses).
	var nilC *Client
	assert.Nil(t, nilC.Informers())
}

func TestClient_StartStopInformersLifecycle(t *testing.T) {
	c := &Client{Clientset: fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}},
	)}

	c.StartInformers()
	require.NotNil(t, c.Informers())
	// Idempotent: second start keeps the same set.
	first := c.Informers()
	c.StartInformers()
	assert.Same(t, first, c.Informers())

	waitFor(t, func() bool { return c.Informers().Pods() != nil }, "pods must sync after start")

	c.StopInformers()
	assert.Nil(t, c.Informers(), "stop must clear the set")
	// Idempotent: double stop is a no-op.
	c.StopInformers()

	// Restartable: a fresh set comes up after a stop (context re-entry).
	c.StartInformers()
	require.NotNil(t, c.Informers())
	waitFor(t, func() bool { return c.Informers().Pods() != nil }, "pods must sync after restart")
	c.StopInformers()
}

func TestManager_ActivateContextExclusive(t *testing.T) {
	mkClient := func() *Client { return &Client{Clientset: fake.NewSimpleClientset()} }
	m := &Manager{Clients: map[string]*Client{
		"prod-a": mkClient(),
		"prod-b": mkClient(),
	}}

	m.ActivateContext("prod-a")
	assert.NotNil(t, m.Clients["prod-a"].Informers(), "active context gets informers")
	assert.Nil(t, m.Clients["prod-b"].Informers(), "inactive context stays cold")

	// Switching activates the new cluster and stops the old one's watches.
	m.ActivateContext("prod-b")
	assert.Nil(t, m.Clients["prod-a"].Informers(), "switch must stop the previous context")
	assert.NotNil(t, m.Clients["prod-b"].Informers())

	// Popping to the picker stops everything.
	m.DeactivateAll()
	assert.Nil(t, m.Clients["prod-a"].Informers())
	assert.Nil(t, m.Clients["prod-b"].Informers())

	// Nil-safety for tests / early paths.
	var nilM *Manager
	nilM.ActivateContext("x")
	nilM.DeactivateAll()
}

// ── Transform hygiene ─────────────────────────────────────────────────
//
// The informer transform strips what the TUI never renders BEFORE objects
// enter the cache: managedFields + last-applied (pure memory weight) and
// Secret/ConfigMap values (memory AND security — payloads must never rest
// in process memory; reveal is a direct Get).

func TestInformers_TransformStripsSecretValues(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("hunter2"),
		},
	})
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	waitFor(t, func() bool { return set.Secrets() != nil }, "secret informer must sync")
	s, err := set.Secrets().Secrets("ns").Get("db-creds")
	require.NoError(t, err)

	// Keys and Type survive (SecretInfo needs exactly that); values do not.
	assert.Equal(t, corev1.SecretTypeOpaque, s.Type)
	assert.Len(t, s.Data, 2)
	assert.Contains(t, s.Data, "username")
	assert.Contains(t, s.Data, "password")
	assert.Nil(t, s.Data["username"], "secret values must be zeroed in cache")
	assert.Nil(t, s.Data["password"], "secret values must be zeroed in cache")
}

func TestInformers_TransformStripsConfigMapValues(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "ns"},
		Data:       map[string]string{"config.yaml": "verbose: true"},
		BinaryData: map[string][]byte{"blob.bin": {0x01, 0x02}},
	})
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	waitFor(t, func() bool { return set.ConfigMaps() != nil }, "configmap informer must sync")
	cm, err := set.ConfigMaps().ConfigMaps("ns").Get("app-config")
	require.NoError(t, err)

	assert.Len(t, cm.Data, 1)
	assert.Empty(t, cm.Data["config.yaml"], "configmap values must be zeroed in cache")
	assert.Len(t, cm.BinaryData, 1)
	assert.Nil(t, cm.BinaryData["blob.bin"], "configmap binary values must be zeroed in cache")
}

func TestInformers_TransformStripsManagedFieldsAndLastApplied(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
			Annotations: map[string]string{
				lastAppliedAnnotation: `{"huge":"blob"}`,
				"keep-me":             "yes",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
	})
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	waitFor(t, func() bool { return set.Pods() != nil }, "pod informer must sync")
	p, err := set.Pods().Pods("ns").Get("p")
	require.NoError(t, err)

	assert.Nil(t, p.ManagedFields, "managedFields must be stripped from cache")
	assert.NotContains(t, p.Annotations, lastAppliedAnnotation)
	assert.Equal(t, "yes", p.Annotations["keep-me"], "unrelated annotations survive")
}

// ── Cache freshness ───────────────────────────────────────────────────
//
// The cache-aware methods on Client read straight from the shared informer's
// lister. If a pod created or deleted after initial sync doesn't propagate,
// the TUI shows stale state — the worst kind of bug because the user trusts
// the table they're looking at. These tests pin live event delivery.

func TestInformerSet_PropagatesPodCreate(t *testing.T) {
	cs := fake.NewSimpleClientset() // empty cluster at sync time
	set := startInformers(cs, "")
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

	waitFor(t, func() bool {
		lister := set.Pods()
		if lister == nil {
			return false
		}
		pods, err := lister.List(labels.Everything())
		return err == nil && len(pods) == 1 && pods[0].Name == "new"
	}, "pod lister must reflect a Create made after initial sync")
}

func TestInformerSet_PropagatesPodDelete(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "doomed", Namespace: "ns"}},
	)
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	// Pre-condition: the lister sees the pod after initial sync.
	waitFor(t, func() bool {
		lister := set.Pods()
		if lister == nil {
			return false
		}
		pods, err := lister.List(labels.Everything())
		return err == nil && len(pods) == 1
	}, "pod lister must see the pod after initial sync")

	// Delete it. The TUI relies on this propagating so the row disappears
	// from the table on the next refresh tick.
	err := cs.CoreV1().Pods("ns").Delete(context.Background(), "doomed", metav1.DeleteOptions{})
	require.NoError(t, err)

	waitFor(t, func() bool {
		pods, err := set.Pods().List(labels.Everything())
		return err == nil && len(pods) == 0
	}, "pod lister must reflect a Delete made after initial sync")
}

func TestInformerSet_PropagatesNodeCreate(t *testing.T) {
	// Same contract for nodes — ClusterHealth iterates this lister.
	cs := fake.NewSimpleClientset()
	set := startInformers(cs, "")
	require.NotNil(t, set)
	t.Cleanup(set.close)

	_, err := cs.CoreV1().Nodes().Create(
		context.Background(),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n-new"}},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	waitFor(t, func() bool {
		lister := set.Nodes()
		if lister == nil {
			return false
		}
		nodes, err := lister.List(labels.Everything())
		return err == nil && len(nodes) == 1
	}, "node lister must reflect a Create made after initial sync")
}
