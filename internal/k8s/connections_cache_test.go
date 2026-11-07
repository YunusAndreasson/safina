package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func connectionCacheObjects() []runtime.Object {
	d := fixtureCheckoutDeployment(1)
	template := d.Spec.Template
	other := d.DeepCopy()
	other.Namespace = "other"
	owned := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "scheduled-child", Namespace: "shop", OwnerReferences: []metav1.OwnerReference{{Kind: "CronJob", Name: "schedule"}}}, Spec: batchv1.JobSpec{Template: template}}
	return []runtime.Object{
		d, other,
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "stateful", Namespace: "shop"}, Spec: appsv1.StatefulSetSpec{Template: template}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "daemon", Namespace: "shop"}, Spec: appsv1.DaemonSetSpec{Template: template}},
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "schedule", Namespace: "shop"}, Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: template}}}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "job", Namespace: "shop"}, Spec: batchv1.JobSpec{Template: template}},
		owned,
	}
}

func waitForConnectionCaches(t *testing.T, client *Client) {
	t.Helper()
	waitFor(t, func() bool {
		inf := client.Informers()
		return inf.Deployments() != nil && inf.StatefulSets() != nil && inf.DaemonSets() != nil && inf.Jobs() != nil && inf.CronJobs() != nil
	}, "workload caches sync")
}

func TestCachedReverseConnectionsMatchDirect(t *testing.T) {
	informerCS := fake.NewSimpleClientset(connectionCacheObjects()...)
	directCS := fake.NewSimpleClientset(connectionCacheObjects()...)
	client := &Client{Clientset: directCS, informerCS: informerCS}
	client.StartInformers()
	t.Cleanup(client.StopInformers)
	waitForConnectionCaches(t, client)
	type resolver func(context.Context, string, string) (Connections, error)
	cases := []struct {
		name    string
		resolve resolver
		direct  resolver
	}{
		{"checkout-config", client.ConnectionsForConfigMap, func(ctx context.Context, ns, name string) (Connections, error) {
			return ConnectionsForConfigMap(ctx, directCS, ns, name)
		}},
		{"checkout-env", client.ConnectionsForSecret, func(ctx context.Context, ns, name string) (Connections, error) {
			return ConnectionsForSecret(ctx, directCS, ns, name)
		}},
		{"checkout-tls", client.ConnectionsForSecret, func(ctx context.Context, ns, name string) (Connections, error) {
			return ConnectionsForSecret(ctx, directCS, ns, name)
		}},
		{"registry-pull", client.ConnectionsForSecret, func(ctx context.Context, ns, name string) (Connections, error) {
			return ConnectionsForSecret(ctx, directCS, ns, name)
		}},
		{"checkout-sa", client.ConnectionsForServiceAccount, func(ctx context.Context, ns, name string) (Connections, error) {
			return ConnectionsForServiceAccount(ctx, directCS, ns, name)
		}},
	}
	dep, err := client.Informers().Deployments().Deployments("shop").Get("checkout")
	require.NoError(t, err)
	before := dep.DeepCopy()
	for _, tc := range cases {
		for _, namespace := range []string{"shop", "other", "empty"} {
			for _, name := range []string{tc.name, "missing"} {
				want, err := tc.direct(t.Context(), namespace, name)
				require.NoError(t, err)
				directCS.ClearActions()
				got, err := tc.resolve(t.Context(), namespace, name)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.Empty(t, directCS.Actions(), "warm reverse lookup must make no API requests")
				if namespace == "shop" && name == tc.name {
					require.Len(t, got.UsedBy, 5, "exclude CronJob-owned Jobs")
				}
			}
		}
	}
	require.Equal(t, before, dep, "shared cached templates must remain unchanged")
	// These lookups need only existing core watches, never auxiliary resources.
	for _, action := range informerCS.Actions() {
		require.NotContains(t, []string{"secrets", "configmaps", "services", "ingresses", "persistentvolumeclaims"}, action.GetResource().Resource)
	}
}

func TestReverseConnectionsFallBackUntilEveryWorkloadCacheIsReady(t *testing.T) {
	cs := fake.NewSimpleClientset(connectionCacheObjects()...)
	client := &Client{Clientset: cs, informerCS: fake.NewSimpleClientset(connectionCacheObjects()...)}
	want, err := ConnectionsForConfigMap(t.Context(), cs, "shop", "checkout-config")
	require.NoError(t, err)
	checkFallback := func(namespace string, expected Connections) {
		t.Helper()
		cs.ClearActions()
		got, lookupErr := client.ConnectionsForConfigMap(t.Context(), namespace, "checkout-config")
		require.NoError(t, lookupErr)
		require.Equal(t, expected, got)
		require.Len(t, cs.Actions(), 5)
		for _, action := range cs.Actions() {
			require.Equal(t, "list", action.GetVerb())
		}
	}
	checkFallback("shop", want) // inactive cluster
	client.StartInformers()
	t.Cleanup(client.StopInformers)
	waitForConnectionCaches(t, client)
	client.Informers().jobs.synced.Store(false) // one required kind still unavailable
	checkFallback("shop", want)
	client.Informers().jobs.synced.Store(true)
	wantAll, err := ConnectionsForConfigMap(t.Context(), cs, "", "checkout-config")
	require.NoError(t, err)
	checkFallback("", wantAll)
}

func TestCachedReverseConnectionsObserveWatchChanges(t *testing.T) {
	cs := fake.NewSimpleClientset(connectionCacheObjects()...)
	client := &Client{Clientset: fake.NewSimpleClientset(), informerCS: cs}
	client.StartInformers()
	t.Cleanup(client.StopInformers)
	waitForConnectionCaches(t, client)
	d, err := cs.AppsV1().Deployments("shop").Get(t.Context(), "checkout", metav1.GetOptions{})
	require.NoError(t, err)
	d.Spec.Template.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{}
	_, err = cs.AppsV1().Deployments("shop").Update(t.Context(), d, metav1.UpdateOptions{})
	require.NoError(t, err)
	waitFor(t, func() bool {
		got, err := client.ConnectionsForConfigMap(t.Context(), "shop", "checkout-config")
		return err == nil && len(got.UsedBy) == 4
	}, "updated template removes a reverse reference")
	require.NoError(t, cs.AppsV1().StatefulSets("shop").Delete(t.Context(), "stateful", metav1.DeleteOptions{}))
	waitFor(t, func() bool {
		got, err := client.ConnectionsForConfigMap(t.Context(), "shop", "checkout-config")
		return err == nil && len(got.UsedBy) == 3
	}, "deleted workload leaves reverse references")
}
