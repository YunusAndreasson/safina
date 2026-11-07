package k8s

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestInformersStartAuxiliaryKindsOnlyOnDemand(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}, Data: map[string][]byte{"key": []byte("private")}})
	set := startInformers(cs)
	t.Cleanup(set.close)
	eager := []string{"pods", "nodes", "namespaces", "deployments", "replicasets", "statefulsets", "daemonsets", "jobs", "cronjobs", "horizontalpodautoscalers"}
	watchCounts := func() map[string]int {
		counts := map[string]int{}
		for _, action := range cs.Actions() {
			if action.GetVerb() == "watch" {
				counts[action.GetResource().Resource]++
			}
		}
		return counts
	}
	waitFor(t, func() bool { return len(watchCounts()) == len(eager) }, "core watches start")
	counts := watchCounts()
	for _, resource := range eager {
		require.Equal(t, 1, counts[resource], resource)
	}
	for _, action := range cs.Actions() {
		require.NotContains(t, []string{"services", "secrets", "configmaps", "persistentvolumeclaims", "ingresses"}, action.GetResource().Resource)
	}
	var readers sync.WaitGroup
	for range 32 {
		readers.Go(func() { set.Secrets() })
	}
	readers.Wait()
	waitFor(t, func() bool { return set.Secrets() != nil }, "secret cache sync")
	secret, err := set.Secrets().Secrets("ns").Get("db")
	require.NoError(t, err)
	require.Contains(t, secret.Data, "key")
	require.Nil(t, secret.Data["key"], "lazy caches retain the stripping transform")
	require.Equal(t, 1, watchCounts()["secrets"], "concurrent access starts exactly one watch")
	require.NotContains(t, watchCounts(), "configmaps")
	// Starting another lazy kind must not restart already-running informers.
	waitFor(t, func() bool { return set.Services() != nil }, "service cache sync")
	counts = watchCounts()
	require.Equal(t, 1, counts["services"])
	require.Equal(t, 1, counts["secrets"])
	for _, resource := range eager {
		require.Equal(t, 1, counts[resource], resource)
	}
	set.close()
	before := len(cs.Actions())
	require.Nil(t, set.ConfigMaps(), "unused caches must not start after close")
	require.Len(t, cs.Actions(), before)
}

func TestInformerCloseIsConcurrentSafe(t *testing.T) {
	set := startInformers(fake.NewSimpleClientset())
	var closers sync.WaitGroup
	for range 32 {
		closers.Go(set.close)
	}
	closers.Wait()
	require.Nil(t, set.Ingresses())
}

func TestLazyInformerDoesNotDelayDirectRead(t *testing.T) {
	informerCS := fake.NewSimpleClientset()
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	informerCS.PrependReactor("list", "secrets", func(ktesting.Action) (bool, runtime.Object, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return false, nil, nil
	})
	directCS := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"}})
	client := &Client{Clientset: directCS, informerCS: informerCS}
	client.StartInformers()
	t.Cleanup(client.StopInformers)
	t.Cleanup(func() { close(release) })
	require.Nil(t, client.Informers().Secrets())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("lazy list did not start")
	}
	type result struct {
		rows []SecretInfo
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := client.ListSecretsAcrossNamespaces(t.Context(), []string{"ns"})
		done <- result{rows, err}
	}()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Len(t, got.rows, 1)
		require.Equal(t, "db", got.rows[0].Name)
	case <-time.After(time.Second):
		t.Fatal("direct read blocked on informer warmup")
	}
}
