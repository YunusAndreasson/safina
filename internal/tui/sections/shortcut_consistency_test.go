package sections

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
)

// Test the actual rendered color escapes, not just the visible key text.
func TestShortcutInkIsSharedAcrossSurfaces(t *testing.T) {
	c := newEventsCtx()
	th := c.Theme
	var metric Metrics
	metric.Select("demo", NodeRef{Kind: k8s.KindPod, Name: "api"}, nil)
	browser := NewBrowser()
	browser.Open(nil, nil)
	cluster := NewClusterSwitcher()
	cluster.Open([]config.ContextInfo{{Name: "demo"}}, "demo")
	cases := []struct{ name, frame, key string }{
		{"metrics", metric.Body(th, 100, 30, time.Now()), "a"},
		{"resource browser", browser.Card(c, 120, 40), "↵"},
		{"cluster switcher", cluster.Card(c, 120, 40, "demo"), "esc"},
		{"tab", RenderWorkspaceTabStrip(th, []WorkspaceTab{WSTabInspector, WSTabLogs}, WSTabLogs, 80), "l"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Contains(t, tc.frame, strings.TrimSuffix(th.HelpKey.Render(tc.key), "\x1b[m"))
		})
	}
}

func TestMetricsChangesAreExplicitlyOptIn(t *testing.T) {
	m, now := metricsFixture()
	m.ShowActivity = false
	body := m.Body(theme.Default(), 100, 30, now)
	require.NotContains(t, body, "Related changes")
	require.NotContains(t, body, "Safina-recorded")
	require.NotContains(t, m.Summary(now), "restart")
	require.Nil(t, m.SelectedAction())
	reload, _ := m.Key("a", now)
	require.True(t, reload)
	require.Contains(t, m.Body(theme.Default(), 100, 30, now), "Related changes")
	require.NotNil(t, m.SelectedAction())
	require.Contains(t, m.Summary(now), "restart")
}

func TestSearchOnlyOffersActionsForResourceResults(t *testing.T) {
	c := newEventsCtx()
	p := NewPalette()
	p.Open([]PaletteEntry{{Kind: "History", Name: "Logbook"}})
	p.ToggleActions()
	require.False(t, p.ActionsActive())
	require.NotContains(t, p.Card(c, 120, 40), "Resource actions")
	p.Open([]PaletteEntry{{Kind: k8s.KindPod, Name: "api"}})
	p.ToggleActions()
	require.True(t, p.ActionsActive())
	card := p.Card(c, 120, 40)
	require.Contains(t, card, strings.TrimSuffix(c.Theme.HelpKey.Render("d"), "\x1b[m"))
	require.Contains(t, card, strings.TrimSuffix(c.Theme.Danger.Render("K"), "\x1b[m"))
}
