package tui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
	"k8s.io/client-go/kubernetes/fake"
)

func warmTestModel(t *testing.T) (*Model, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset()
	client := &k8s.Client{Clientset: cs}
	client.StartInformers()
	t.Cleanup(client.StopInformers)
	m := NewModel(&k8s.Manager{Clients: map[string]*k8s.Client{"test": client}}, &config.AppConfig{EnabledResources: config.DefaultEnabledResources()})
	t.Cleanup(m.ctx.Cancel)
	m.ctx.ActiveContext = "test"
	m.tree.NavigateTo([]sections.NodeRef{{Kind: k8s.KindContext, Name: "test"}, {Kind: k8s.KindNamespaceFolder, Name: "ns", Namespace: "ns"}})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp})
	m.tree.SetCursorByRef(k8s.KindSecretsFold, "", "Secrets")
	require.Equal(t, k8s.KindSecretsFold, m.tree.SelectedRef().Kind)
	return &m, cs
}

func auxiliaryWatches(cs *fake.Clientset) map[string]int {
	counts := map[string]int{}
	for _, a := range cs.Actions() {
		if a.GetVerb() == "watch" {
			switch r := a.GetResource().Resource; r {
			case "secrets", "services", "configmaps", "ingresses", "persistentvolumeclaims":
				counts[r]++
			}
		}
	}
	return counts
}

func TestCacheWarmQuietSelection(t *testing.T) {
	m, cs := warmTestModel(t)
	before := m.tree.SelectedRef()
	cmd := m.scheduleCacheWarm()
	require.NotNil(t, cmd)
	require.Nil(t, m.scheduleCacheWarm(), "stationary selection must not enqueue another timer")
	require.Empty(t, auxiliaryWatches(cs))
	start := time.Now()
	msg, ok := cmd().(cacheWarmMsg)
	require.True(t, ok)
	require.GreaterOrEqual(t, time.Since(start), cacheWarmDelay)
	require.Empty(t, auxiliaryWatches(cs), "timer expiration alone must not start a cache")
	warm := m.handleCacheWarm(msg)
	require.NotNil(t, warm)
	require.Nil(t, m.handleCacheWarm(msg), "consume queued messages only once")
	warm()
	require.Eventually(t, func() bool { return auxiliaryWatches(cs)["secrets"] == 1 }, time.Second*3, time.Millisecond*10)
	require.Equal(t, map[string]int{"secrets": 1}, auxiliaryWatches(cs))
	require.Equal(t, before, m.tree.SelectedRef())
	require.Nil(t, m.scheduleCacheWarm())
}

func TestCacheWarmCancelsOnMovement(t *testing.T) {
	m, cs := warmTestModel(t)
	old := m.scheduleCacheWarm()
	m.tree.SetCursorByRef(k8s.KindNetworkFold, "", "Services")
	next := m.scheduleCacheWarm()
	require.Nil(t, old(), "moving cancels pending timer")
	msg, ok := next().(cacheWarmMsg)
	require.True(t, ok)
	warm := m.handleCacheWarm(msg)
	require.NotNil(t, warm)
	m.tree.SetCursorByRef(k8s.KindSecretsFold, "", "Secrets")
	require.NotNil(t, m.scheduleCacheWarm())
	warm()
	require.Empty(t, auxiliaryWatches(cs), "moving also cancels a queued warm command")
	require.Nil(t, m.handleCacheWarm(msg), "old message must not affect current timer")
}

func TestCacheWarmRejectsStaleFocus(t *testing.T) {
	for _, change := range []string{"blur", "tree", "context", "cancel"} {
		t.Run(change, func(t *testing.T) {
			m, cs := warmTestModel(t)
			msg, ok := m.scheduleCacheWarm()().(cacheWarmMsg)
			require.True(t, ok)
			switch change {
			case "blur":
				m.focused = false
			case "tree":
				m.tree.Focused = false
			case "context":
				m.ctx.ActiveContext = "other"
			case "cancel":
				m.ctx.Cancel()
			}
			require.Nil(t, m.handleCacheWarm(msg))
			require.Empty(t, auxiliaryWatches(cs))
		})
	}
}
