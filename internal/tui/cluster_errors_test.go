package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func enteringClusterModel(t *testing.T) *Model {
	t.Helper()
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.focused = true
	m.ctx.ActiveContext = "demo"
	m.tree.PushContext("demo")
	return &m
}

// The 5s tick used to start a refresh while the cluster's first load was in
// flight, cancelling that load: any cluster slower than one tick lost its
// first five seconds.
func TestRefreshTickLeavesTheFirstLoadAlone(t *testing.T) {
	m := enteringClusterModel(t)
	m.tree.Loading = true
	m.Update(refreshMsg{})
	require.False(t, m.tree.Refreshing, "the tick must not restart a load in flight")

	m.tree.Loading = false
	m.Update(refreshMsg{})
	require.True(t, m.tree.Refreshing, "once loaded, the tick refreshes as before")
}

// A first load that failed shows its error and r retry in the body. The tick
// used to re-run it every 5s, forever, against a cluster that had rejected
// the login. A failed refresh behind stale rows still retries on its own.
func TestRefreshTickLeavesAFailedFirstLoadToTheRetryKey(t *testing.T) {
	m := enteringClusterModel(t)
	m.tree.LoadErr = apierrors.NewUnauthorized("Unauthorized")
	m.Update(refreshMsg{})
	require.False(t, m.tree.Refreshing)

	m = enteringClusterModel(t)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindContext})
	m.tree.LoadErr = apierrors.NewUnauthorized("Unauthorized")
	m.Update(refreshMsg{})
	require.True(t, m.tree.Refreshing, "stale rows on screen: keep trying to replace them")
}

// The Logs, Events and YAML tabs leave the tree and topbar on screen; they
// used to stop the refresh, so pod status and metrics froze beside the logs.
func TestRefreshTickKeepsTheTreeLiveBesideReadingTabs(t *testing.T) {
	for _, st := range []state{stateLogView, stateEventsView, stateDescribeView} {
		m := enteringClusterModel(t)
		m.state = st
		m.Update(refreshMsg{})
		require.True(t, m.tree.Refreshing, "state %d", st)
	}
	m := enteringClusterModel(t)
	m.state = stateHelp
	m.Update(refreshMsg{})
	require.False(t, m.tree.Refreshing, "a modal over the tree still pauses it")
}

// A cluster that doesn't answer fails with the client's deadline. That used
// to read as a cancellation, so the error was dropped and the body spun on
// "loading resources" for as long as safina ran.
func TestTimedOutLoadShowsTheError(t *testing.T) {
	m := enteringClusterModel(t)
	m.tree.Loading = true
	timeout := fmt.Errorf("Get \"https://10.0.0.1/api/v1/namespaces\": dial tcp 10.0.0.1:443: %w", context.DeadlineExceeded)
	m.Update(sections.TreeChildrenLoadedMsg{Path: m.tree.Path(), Err: timeout})
	require.ErrorIs(t, m.tree.LoadErr, context.DeadlineExceeded)
	require.Contains(t, ansi.Strip(m.tree.View(m.ctx, 80, 20)), "dial tcp 10.0.0.1:443")
}

// One failure, one message. A first load that fails shows its error in the
// body, so it stays out of the banner (but is kept in the notifications with
// its retry); only a failed refresh behind stale rows needs the banner.
// Discovery that got nothing hit the same wall as the load and stays quiet;
// discovery with a partial list is reporting a broken API group.
func TestOneClusterFailureIsReportedOnce(t *testing.T) {
	unreachable := fmt.Errorf("dial tcp 10.0.0.1:443: %w", context.DeadlineExceeded)

	m := enteringClusterModel(t)
	m.Update(sections.TreeChildrenLoadedMsg{Path: m.tree.Path(), Err: unreachable})
	require.Empty(t, m.bannerMsg, "the body already shows a first-load failure")
	require.NotEmpty(t, m.notifications)
	require.NotNil(t, m.notifications[len(m.notifications)-1].retry)

	m.Update(sections.DiscoveryLoadedMsg{Context: "demo", Err: unreachable})
	require.Empty(t, m.bannerMsg, "discovery with no kinds failed for the same reason")

	m.Update(sections.DiscoveryLoadedMsg{Context: "demo", Kinds: []k8s.ResourceKind{{Kind: "Pod"}}, Err: errors.New("metrics.k8s.io/v1beta1: the server is currently unable to handle the request")})
	require.Contains(t, m.bannerMsg, "metrics.k8s.io")

	stale := enteringClusterModel(t)
	stale.tree.SetChildren(stale.tree.Path(), &sections.Children{Kind: k8s.KindNamespaceFolder})
	stale.Update(sections.TreeChildrenLoadedMsg{Path: stale.tree.Path(), Err: unreachable})
	require.Contains(t, stale.bannerMsg, "dial tcp 10.0.0.1:443", "stale rows would hide the failure without the banner")
}

func TestClusterLoadErrorsSayWhatWentWrong(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "rejected credentials",
			err:  apierrors.NewUnauthorized("Unauthorized"),
			want: []string{"Credentials rejected", "Log in again"},
		},
		{
			name: "namespaces denied across the cluster",
			err:  apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "", errors.New("cannot list at the cluster scope")),
			want: []string{"Permission denied", "--namespace"},
		},
		{
			name: "other kinds denied",
			err:  apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("cannot list")),
			want: []string{"Permission denied", "Check Kubernetes access"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := enteringClusterModel(t)
			m.tree.LoadErr = tc.err
			body := ansi.Strip(m.tree.View(m.ctx, 100, 20))
			for _, want := range tc.want {
				require.Contains(t, body, want)
			}
			if tc.name == "rejected credentials" {
				require.NotContains(t, body, "Permission denied")
			}
		})
	}
}

// get-credentials that adds a context while safina runs: the cluster switcher
// lists it at once instead of after a restart.
func TestClusterSwitcherListsAContextAddedWhileRunning(t *testing.T) {
	m := enteringClusterModel(t)
	m.SetContexts([]config.ContextInfo{{Name: "demo"}})
	now := clientcmdapi.NewConfig()
	for _, name := range []string{"demo", "fresh"} {
		now.Contexts[name] = &clientcmdapi.Context{Cluster: name, AuthInfo: name}
	}
	m.ctx.Manager.SetKubeconfigLoader(func() (*clientcmdapi.Config, error) { return now, nil })
	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	require.Equal(t, stateClusters, m.state)
	require.Contains(t, ansi.Strip(m.View().Content), "fresh")
}
