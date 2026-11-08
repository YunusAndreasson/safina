package sections

import (
	"context"
	"encoding/json"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestHistoryRecordsFailureAndIdentityBeforeMutation(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	cs := fake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "pod-original"}})
	mgr := &k8s.Manager{History: store, Clients: map[string]*k8s.Client{"prod": {Clientset: cs}}}
	cs.PrependReactor("delete", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		r, e := history.Query(store.Directory(), history.Filter{Cluster: "prod"})
		require.NoError(t, e)
		require.NotEmpty(t, r.Records)
		require.Equal(t, "requested", r.Records[0].Outcome)
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "api", nil)
	})
	msg, ok := DeleteResource(t.Context(), mgr, "prod", "Pod", "shop", "api")().(ActionDoneMsg)
	require.True(t, ok)
	require.Error(t, msg.Err)
	r, err := history.Query(store.Directory(), history.Filter{Cluster: "prod"})
	require.NoError(t, err)
	last := r.Records[len(r.Records)-1]
	require.Equal(t, "failed", last.Outcome)
	require.Equal(t, types.UID("pod-original"), last.Target.UID)
	require.Equal(t, "shop", last.Target.Namespace)
	require.Contains(t, last.Detail, "Forbidden")
}

func TestHistoryRestartUsesMutationResponseAndSecretRevealOmitsValues(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "deployment-original", Generation: 4}}
	cs := fake.NewClientset(d, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: "shop"}, Data: map[string][]byte{"password": []byte("do-not-save-this")}})
	cs.PrependReactor("patch", "deployments", func(ktesting.Action) (bool, runtime.Object, error) {
		out := d.DeepCopy()
		out.Generation = 5
		return true, out, nil
	})
	mgr := &k8s.Manager{History: store, Clients: map[string]*k8s.Client{"prod": {Clientset: cs}}}
	msg, ok := RestartApp(t.Context(), mgr, "prod", "shop", "Deployment", "api")().(ActionDoneMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.Equal(t, "5", msg.Generation, "later GET still returns generation 4; acceptance must use response")
	require.Equal(t, types.UID("deployment-original"), msg.Operation.Target.UID)
	reveal, ok := LoadSecretReveal(t.Context(), mgr, "prod", "shop", "auth")().(DescribeLoadedMsg)
	require.True(t, ok)
	require.NoError(t, reveal.Err)
	require.Contains(t, reveal.YAML, "do-not-save-this")
	r, err := history.Query(store.Directory(), history.Filter{})
	require.NoError(t, err)
	b, err := json.Marshal(r)
	require.NoError(t, err)
	require.NotContains(t, string(b), "do-not-save-this")
	for _, record := range r.Records {
		require.NotEqual(t, "completed", record.Outcome, "restart API acceptance is not readiness; secret read is completed")
		if record.Action == "reveal_secret" {
			break
		}
	}
}

func TestHistoryCancelledActionKeepsOutcomeUncertain(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	cs := fake.NewClientset()
	cs.PrependReactor("delete", "pods", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, context.Canceled })
	mgr := &k8s.Manager{History: store, Clients: map[string]*k8s.Client{"prod": {Clientset: cs}}}
	_ = DeleteResource(t.Context(), mgr, "prod", "Pod", "shop", "api")()
	result, err := history.Query(store.Directory(), history.Filter{Cluster: "prod"})
	require.NoError(t, err)
	last := result.Records[len(result.Records)-1]
	require.Equal(t, "cancelled", last.Outcome)
	require.Contains(t, last.Detail, "unknown")
}
