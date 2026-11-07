package sections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestNamespaceSearchScopesReadsAndKeepsPartialResults(t *testing.T) {
	cs := fake.NewClientset(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "shop"}}, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "private"}})
	cs.PrependReactor("list", "secrets", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("forbidden") })
	mgr := &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Clientset: cs}}}
	path := []NodeRef{{Kind: k8s.KindContext, Name: "demo"}, {Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"}}
	msg, ok := SearchNamespace(t.Context(), mgr, "demo", "shop", path, 4)().(NamespaceSearchMsg)
	require.True(t, ok)
	require.EqualValues(t, 4, msg.Request)
	require.ErrorContains(t, msg.Err, "forbidden")
	require.Len(t, msg.Entries, 1)
	require.Equal(t, "settings", msg.Entries[0].Name)
	for _, action := range cs.Actions() {
		require.Equal(t, "shop", action.GetNamespace())
		require.Equal(t, "list", action.GetVerb())
	}
	p := NewPalette()
	p.Open(nil)
	p.SetQuery("settings")
	p.SetEntries(msg.Entries)
	require.Equal(t, "settings", p.Query())
	_, ok = p.Selected()
	require.True(t, ok)
}
