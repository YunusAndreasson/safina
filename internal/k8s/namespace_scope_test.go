package k8s

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// tenantCluster is a fake cluster whose account may read only the "tenant"
// namespace: any List or Watch across the cluster gets a 403, and is counted.
func tenantCluster(objects ...runtime.Object) (*fake.Clientset, *atomic.Int32) {
	cs := fake.NewSimpleClientset(objects...)
	var clusterWide atomic.Int32
	deny := func(action k8stesting.Action) error {
		clusterWide.Add(1)
		gr := action.GetResource().GroupResource()
		return apierrors.NewForbidden(schema.GroupResource{Group: gr.Group, Resource: gr.Resource}, "", nil)
	}
	cs.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() != "" {
			return false, nil, nil
		}
		return true, nil, deny(action)
	})
	cs.PrependWatchReactor("*", func(action k8stesting.Action) (bool, watch.Interface, error) {
		if action.GetNamespace() != "" {
			return false, nil, nil
		}
		return true, nil, deny(action)
	})
	return cs, &clusterWide
}

// Once the namespace list comes back forbidden, the running watches move
// into the namespace (where the account may watch) and the cluster-health
// tick stops asking for nodes and pods it can't see.
func TestNamespaceScopedAccountStopsAskingAcrossTheCluster(t *testing.T) {
	cs, clusterWide := tenantCluster(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "tenant"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-1", Namespace: "other"}},
	)
	ctx := context.Background()
	c := &Client{Clientset: cs}
	c.SetNamespace("tenant")
	c.StartInformers()
	t.Cleanup(c.StopInformers)

	_, _, err := c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return c.Informers().Pods() != nil }, 2*time.Second, 5*time.Millisecond,
		"pods must sync once the watch is scoped to the namespace")
	pods, err := c.Informers().Pods().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, pods, 1)
	require.Equal(t, "web-1", pods[0].Name)
	require.Nil(t, c.Informers().Nodes(), "a scoped set has no node cache")

	before := clusterWide.Load()
	health, err := c.LoadClusterHealth(ctx)
	require.NoError(t, err)
	require.False(t, health.HasData())
	_, _, err = c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, before, clusterWide.Load(), "no request may leave the namespace any more")
}

// An account bound to one namespace (a tenant token, say) can't list
// anything across the cluster, but reads its own namespace fine. Safina used
// to open the cluster with a cluster-wide namespace list and stop at the 403.
func TestNamespaceScopedAccountOpensItsNamespace(t *testing.T) {
	replicas := int32(1)
	cs := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "tenant"},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		},
	)
	clusterLists := 0
	cs.PrependReactor("list", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() != "" {
			return false, nil, nil
		}
		gr := action.GetResource().GroupResource()
		if gr.Resource == "namespaces" {
			clusterLists++
		}
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: gr.Group, Resource: gr.Resource}, "", nil)
	})
	ctx := context.Background()

	c := &Client{Clientset: cs}
	_, _, err := c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.True(t, apierrors.IsForbidden(err), "with no namespace to fall back on, the 403 stands")

	c.SetNamespace("tenant")
	user, system, err := c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)
	require.Empty(t, system)
	require.Len(t, user, 1)
	require.Equal(t, "tenant", user[0].Name)

	_, _, err = c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 2, clusterLists, "after one 403 with a namespace set, stop asking for the cluster-wide list")
	namespaceGets := 0
	for _, a := range cs.Actions() {
		if a.GetVerb() == "get" && a.GetResource().Resource == "namespaces" {
			namespaceGets++
		}
	}
	require.Equal(t, 1, namespaceGets, "the namespace's age is asked for once, not on every tick")

	apps, err := c.ListAppsInNamespaces(ctx, []string{user[0].Name})
	require.NoError(t, err)
	require.Len(t, apps, 1)
	require.Equal(t, "web", apps[0].Name)
}

func TestKubeconfigContextNamespaceScopesTheClient(t *testing.T) {
	c, err := NewClient(testKubeconfig("pinned", "tenant"), "pinned")
	require.NoError(t, err)
	require.Equal(t, "tenant", c.Namespace())

	c, err = NewClient(testKubeconfig("open", ""), "open")
	require.NoError(t, err)
	require.Empty(t, c.Namespace())
	require.NotNil(t, c.RESTConfig.Dial, "connects are bounded by connectTimeout, not the 30s request timeout")
}

func testKubeconfig(contextName, namespace string) *clientcmdapi.Config {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["cluster"] = &clientcmdapi.Cluster{Server: "https://127.0.0.1:6443"}
	cfg.AuthInfos["user"] = &clientcmdapi.AuthInfo{Token: "test-token"}
	cfg.Contexts[contextName] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "user", Namespace: namespace}
	return cfg
}
