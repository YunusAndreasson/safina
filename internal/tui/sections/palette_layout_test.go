package sections

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestPaletteFitsSmallTerminalsAndLongQueries(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 18}, {50, 14}} {
		p := NewPalette()
		entries := make([]PaletteEntry, 60)
		for i := range entries {
			entries[i] = PaletteEntry{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Name: strings.Repeat("long-name-", 10), Namespace: "long-namespace"}
		}
		p.Open(entries)
		p.ToggleActions()
		for _, query := range []string{"", strings.Repeat("absent", 50)} {
			p.SetQuery(query)
			view := p.View(newEventsCtx(), size[0], size[1])
			require.LessOrEqual(t, lipgloss.Width(view), size[0])
			require.LessOrEqual(t, lipgloss.Height(view), size[1], "%s", ansi.Strip(view))
			require.Contains(t, ansi.Strip(view), "esc close")
		}
	}
}

func TestPaletteIndexIsStableAndPreservesWorkloadKinds(t *testing.T) {
	cache := map[string]*Children{
		"Context//demo//|NamespaceFolder/shop/shop//":   {Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "same", Namespace: "shop"}, {Kind: k8s.AppKindStatefulSet, Name: "same", Namespace: "shop"}}},
		"Context//demo//|NamespaceFolder/other/other//": {Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "aaa", Namespace: "other"}}},
	}
	expected := BuildPaletteIndex(cache)
	require.Len(t, expected, 3)
	require.Equal(t, "aaa", expected[0].Name)
	for range 20 {
		require.Equal(t, expected, BuildPaletteIndex(cache))
	}
}

func TestBrowserAndClusterSwitcherFitLongSearches(t *testing.T) {
	c := newEventsCtx()
	b := NewBrowser()
	b.Open(nil, nil)
	s := NewClusterSwitcher()
	s.Open(nil, "")
	for _, size := range [][2]int{{80, 24}, {60, 18}, {50, 14}} {
		for _, query := range []string{"", strings.Repeat("long-query-", 30)} {
			b.SetQuery(query)
			s.SetQuery(query)
			for _, view := range []string{b.View(c, size[0], size[1]), s.View(c, size[0], size[1], "")} {
				require.LessOrEqual(t, lipgloss.Width(view), size[0])
				require.LessOrEqual(t, lipgloss.Height(view), size[1])
				require.Regexp(t, "esc (cancel|back)", ansi.Strip(view))
			}
		}
	}
}
