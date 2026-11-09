package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// A Kubernetes 1.20 server: CronJobs only in
// batch/v1beta1, HPAs only in autoscaling/v1.
func oldServer(t *testing.T) *fake.Clientset {
	t.Helper()
	replicas := int32(2)
	minReplicas := int32(1)
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}},
		&batchv1beta1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "shop"}, Spec: batchv1beta1.CronJobSpec{Schedule: "0 3 * * *"}},
		&autoscalingv1.HorizontalPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"},
			Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
				ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{Kind: "Deployment", Name: "api"},
				MinReplicas:    &minReplicas, MaxReplicas: 4,
			},
			Status: autoscalingv1.HorizontalPodAutoscalerStatus{CurrentReplicas: 2},
		},
	)
	disco, ok := cs.Discovery().(*fakediscovery.FakeDiscovery)
	require.True(t, ok)
	disco.Resources = []*metav1.APIResourceList{
		{GroupVersion: "batch/v1", APIResources: []metav1.APIResource{{Name: "jobs"}}},
		{GroupVersion: "batch/v1beta1", APIResources: []metav1.APIResource{{Name: "cronjobs"}}},
		{GroupVersion: "autoscaling/v1", APIResources: []metav1.APIResource{{Name: "horizontalpodautoscalers"}}},
	}
	notServed := func(group, version, resource string) {
		cs.PrependReactor("list", resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
			gvr := action.GetResource()
			if gvr.Group != group || gvr.Version != version {
				return false, nil, nil
			}
			return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: group, Resource: resource}, "")
		})
	}
	notServed("batch", "v1", "cronjobs")
	notServed("autoscaling", "v2", "horizontalpodautoscalers")
	return cs
}

func appNamed(apps []AppInfo, name, kind string) *AppInfo {
	for i := range apps {
		if apps[i].Name == name && apps[i].Kind == kind {
			return &apps[i]
		}
	}
	return nil
}

func TestOldServerShowsCronJobsAndHPAsDirect(t *testing.T) {
	apps, err := ListAppsInNamespaces(context.Background(), oldServer(t), []string{"shop"})
	require.NoError(t, err)
	require.NotNil(t, appNamed(apps, "nightly", AppKindCronJob), "batch/v1beta1 CronJobs must show")
	api := appNamed(apps, "api", AppKindDeployment)
	require.NotNil(t, api)
	require.NotNil(t, api.HPA, "the autoscaling/v1 HPA must badge its Deployment")
	require.Equal(t, int32(4), api.HPA.Max)
}

// Once the caches are warm, the old versions are watched too, so the 5s tick
// lists nothing: before, the v1 CronJob and v2 HPA caches never synced and
// every kind went back to the network on every tick.
func TestOldServerServesTheTreeFromCache(t *testing.T) {
	cs := oldServer(t)
	c := &Client{Clientset: cs}
	c.StartInformers()
	t.Cleanup(c.StopInformers)
	require.Eventually(t, func() bool {
		inf := c.Informers()
		return inf.Pods() != nil && inf.Deployments() != nil && inf.StatefulSets() != nil && inf.DaemonSets() != nil &&
			inf.Jobs() != nil && inf.CronJobs() != nil && inf.HPAs() != nil
	}, 2*time.Second, 5*time.Millisecond)

	cs.ClearActions()
	apps, err := c.ListAppsInNamespaces(context.Background(), []string{"shop"})
	require.NoError(t, err)
	require.NotNil(t, appNamed(apps, "nightly", AppKindCronJob))
	require.NotNil(t, appNamed(apps, "api", AppKindDeployment).HPA)
	for _, a := range cs.Actions() {
		require.NotEqual(t, "list", a.GetVerb(), "warm caches must serve %s", a.GetResource().Resource)
	}
}

// A kind the account may not list in its namespace is asked for once, then
// left alone until unavailableFor has passed.
func TestForbiddenKindIsNotAskedForEveryTick(t *testing.T) {
	cs := fake.NewSimpleClientset()
	daemonSetLists := 0
	cs.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		daemonSetLists++
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "daemonsets"}, "", nil)
	})
	c := &Client{Clientset: cs}
	for range 3 {
		_, err := c.ListAppsInNamespaces(context.Background(), []string{"tenant"})
		require.NoError(t, err)
	}
	require.Equal(t, 1, daemonSetLists)
}
