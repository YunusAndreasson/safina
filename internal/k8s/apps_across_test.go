package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func appsFixture() *fake.Clientset {
	objs := make([]runtime.Object, 0, 4)
	for _, ns := range []string{"a", "b", "c", "kube-system"} {
		objs = append(objs, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: ns}})
	}
	return fake.NewSimpleClientset(objs...)
}

// listsOf counts List actions per resource and namespace.
func listsOf(cs *fake.Clientset) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, a := range cs.Actions() {
		if a.GetVerb() != "list" {
			continue
		}
		r := a.GetResource().Resource
		if out[r] == nil {
			out[r] = map[string]int{}
		}
		out[r][a.GetNamespace()]++
	}
	return out
}

// Without caches, workloads for several namespaces cost one List per kind,
// not one per kind per namespace, and only the asked namespaces come back.
func TestAppsAcrossNamespacesListEachKindOnce(t *testing.T) {
	cs := appsFixture()
	apps, err := listApps(t.Context(), cs, []string{"a", "b", "c"}, appSources{gate: &readGate{}})
	require.NoError(t, err)
	got := make([]string, 0, len(apps))
	for _, a := range apps {
		got = append(got, a.Namespace)
	}
	require.ElementsMatch(t, []string{"a", "b", "c"}, got, "kube-system was not asked for")
	lists := listsOf(cs)
	for _, r := range []string{"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "horizontalpodautoscalers"} {
		require.Equal(t, map[string]int{"": 1}, lists[r], r)
	}

	single := appsFixture()
	_, err = listApps(t.Context(), single, []string{"b"}, appSources{gate: &readGate{}})
	require.NoError(t, err)
	require.Equal(t, map[string]int{"b": 1}, listsOf(single)["deployments"], "one namespace reads only that namespace")
}

// An account that may read only its namespaces gets them one by one, and
// the denied cluster-wide read is not repeated on the next refresh.
func TestAppsAcrossNamespacesFallBackWhenForbidden(t *testing.T) {
	cs := appsFixture()
	cs.PrependReactor("list", "*", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: a.GetResource().Resource}, "", nil)
		}
		return false, nil, nil
	})
	gate := &readGate{}
	for range 2 {
		apps, err := listApps(t.Context(), cs, []string{"a", "b", "c"}, appSources{gate: gate})
		require.NoError(t, err)
		require.Len(t, apps, 3)
	}
	lists := listsOf(cs)["deployments"]
	require.Equal(t, 1, lists[""], "the forbidden cluster-wide read is tried once")
	require.Equal(t, map[string]int{"": 1, "a": 2, "b": 2, "c": 2}, lists)
}
