package tui

import (
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func overviewTestModel(t *testing.T) (*Model, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "fresh"}})
	client := &k8s.Client{Clientset: cs, RESTConfig: &rest.Config{Host: "https://cluster.test"}}
	m := NewModel(&k8s.Manager{Clients: map[string]*k8s.Client{"test": client}}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.overviewStore = &overviewStore{dir: t.TempDir()}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.ctx.ActiveContext = "test"
	m.tree.NavigateTo([]sections.NodeRef{{Kind: k8s.KindContextList, Name: "Clusters"}, {Kind: k8s.KindContext, Name: "test"}})
	return &m, cs
}

func TestOverviewPreviewShowsWhileFreshReadIsBlocked(t *testing.T) {
	m, cs := overviewTestModel(t)
	snapshot := overviewFixture(m.overviewKey(), time.Now().Add(-time.Hour))
	m.overviewStore.save(snapshot)
	// Force the read to exercise disk rather than the in-memory fast path.
	m.overviewStore = &overviewStore{dir: m.overviewStore.dir}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	cs.PrependReactor("list", "namespaces", func(ktesting.Action) (bool, runtime.Object, error) {
		close(entered)
		<-release
		return false, nil, nil
	})
	batch, ok := m.loadTreeFocus()().(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batch, 2)
	fresh := make(chan tea.Msg, 1)
	go func() { fresh <- batch[0]() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("fresh read did not start")
	}
	preview := batch[1]()
	m.Update(preview)
	require.True(t, m.overviewPreview)
	require.True(t, m.dataLooksStale())
	require.Contains(t, m.View().Content, "cached")
	require.Equal(t, "shop", m.tree.Children().UserNS[0].Name)
	require.False(t, m.tree.Loading)
	require.True(t, m.tree.Refreshing)
	unblock()
	select {
	case msg := <-fresh:
		m.Update(msg)
	case <-time.After(3 * time.Second):
		t.Fatal("fresh read did not complete")
	}
	require.False(t, m.overviewPreview)
	require.Equal(t, "fresh", m.tree.Children().UserNS[0].Name)
	// A cached result delivered after the fresh one cannot replace it.
	m.Update(preview)
	require.Equal(t, "fresh", m.tree.Children().UserNS[0].Name)
}

func TestOverviewPreviewRejectsStaleRequestsAndSurvivesReadFailure(t *testing.T) {
	m, _ := overviewTestModel(t)
	m.treeLoadEpoch = 2
	m.overviewPending = true
	snapshot := overviewFixture(m.overviewKey(), time.Now().Add(-time.Minute))
	path := append([]sections.NodeRef(nil), m.tree.Path()...)
	m.Update(overviewPreviewMsg{epoch: 1, path: path, snapshot: &snapshot})
	require.Nil(t, m.tree.Children())
	m.Update(overviewPreviewMsg{epoch: 2, path: path, snapshot: &snapshot})
	require.NotNil(t, m.tree.Children())
	m.Update(treeLoadResultMsg{epoch: 1, result: sections.TreeChildrenLoadedMsg{Path: path, Children: &sections.Children{Kind: k8s.KindNamespaceFolder}}})
	require.Equal(t, "shop", m.tree.Children().UserNS[0].Name)
	m.Update(treeLoadResultMsg{epoch: 2, result: sections.TreeChildrenLoadedMsg{Path: path, Err: errors.New("offline")}})
	require.True(t, m.overviewPreview)
	require.Error(t, m.tree.LoadErr)
	require.Equal(t, "shop", m.tree.Children().UserNS[0].Name)
	// Leaving a preview does not seed the root navigation cache with stale data.
	m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	m.tree.Pop()
	require.Nil(t, m.tree.Children())
}

func TestOverviewKeySeparatesClusterAndNamespaceConfiguration(t *testing.T) {
	m, _ := overviewTestModel(t)
	original := m.overviewKey()
	require.NotEmpty(t, original)
	client, ok := m.ctx.Manager.GetClient("test")
	require.True(t, ok)
	client.RESTConfig.Host = "https://other.test"
	require.NotEqual(t, original, m.overviewKey())
	client.RESTConfig.Host = "https://cluster.test"
	client.RESTConfig.Username = "other-user"
	require.NotEqual(t, original, m.overviewKey())
	client.RESTConfig.Username = ""
	client.RESTConfig.Impersonate.UserName = "other-identity"
	require.NotEqual(t, original, m.overviewKey())
	client.RESTConfig.Impersonate.UserName = ""
	m.ctx.AppCfg.UserNamespaces = []string{"different"}
	require.NotEqual(t, original, m.overviewKey())
}

func TestOverviewRejectsLateClusterNavigation(t *testing.T) {
	m, _ := overviewTestModel(t)
	path := append([]sections.NodeRef(nil), m.tree.Path()...)
	path[len(path)-1].Name = "old-cluster"
	_, cmd := m.Update(sections.TreeFocusChangedMsg{Path: path})
	require.Nil(t, cmd)
	require.Equal(t, "test", m.ctx.ActiveContext)
	require.Equal(t, "test", m.tree.Focus().Name)
	_, cmd = m.Update(sections.TreeFocusChangedMsg{})
	require.Nil(t, cmd)
}
