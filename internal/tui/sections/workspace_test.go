package sections

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// TestAvailableTabs_OrderAndPresence pins the tab order across every
// permutation we care about: Inspector is always first, then the standard
// data verbs (Logs/Events/Yaml) appear in source order, then Chat, then
// pinned ops (Hot/Forwards/Drain). A regression that moves Chat into the
// pinned group would silently reshuffle the strip.
func TestAvailableTabs_OrderAndPresence(t *testing.T) {
	cases := map[string]struct {
		avail WSTabAvailability
		want  []WorkspaceTab
	}{
		"empty availability — only Inspector": {
			avail: WSTabAvailability{},
			want:  []WorkspaceTab{WSTabInspector},
		},
		"all standard tabs": {
			avail: WSTabAvailability{Logs: true, Events: true, Yaml: true, Chat: true},
			want:  []WorkspaceTab{WSTabInspector, WSTabLogs, WSTabEvents, WSTabYaml, WSTabChat},
		},
		"only pinned ops shown": {
			avail: WSTabAvailability{Hot: true, Forwards: true, Drain: true},
			want:  []WorkspaceTab{WSTabInspector, WSTabHot, WSTabForwards, WSTabDrain},
		},
		"chat sits between Yaml and pinned": {
			avail: WSTabAvailability{Yaml: true, Chat: true, Hot: true},
			want:  []WorkspaceTab{WSTabInspector, WSTabYaml, WSTabChat, WSTabHot},
		},
		"logs without events": {
			avail: WSTabAvailability{Logs: true},
			want:  []WorkspaceTab{WSTabInspector, WSTabLogs},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, AvailableTabs(tc.avail))
		})
	}
}

// TestWorkspaceTabHitboxes_Disjoint asserts that the per-tab x-ranges
// returned by the hitbox helper are non-overlapping and ascending, so
// the mouse-click router doesn't route a single column to two tabs.
func TestWorkspaceTabHitboxes_Disjoint(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Logs: true, Events: true, Yaml: true, Chat: true})
	hits := WorkspaceTabHitboxes(tabs, WSTabInspector, 200)
	require.Len(t, hits, len(tabs))
	for i := 1; i < len(hits); i++ {
		assert.GreaterOrEqual(t, hits[i].X0, hits[i-1].X1,
			"tab %d (%v) overlaps tab %d (%v): %+v vs %+v",
			i, hits[i].Tab, i-1, hits[i-1].Tab, hits[i], hits[i-1])
	}
	for _, h := range hits {
		assert.Greater(t, h.X1, h.X0, "every hitbox must have width > 0")
	}
}

// TestWorkspaceTabHitboxes_TruncatesPastWidth pins the documented
// degradation: when the strip would overflow, the returned hitboxes
// stop at the visible region rather than reporting unreachable ranges.
func TestWorkspaceTabHitboxes_TruncatesPastWidth(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Logs: true, Events: true, Yaml: true, Chat: true, Hot: true, Forwards: true, Drain: true})
	full := WorkspaceTabHitboxes(tabs, WSTabInspector, 200)
	require.Len(t, full, len(tabs))

	narrow := WorkspaceTabHitboxes(tabs, WSTabInspector, 30)
	assert.Less(t, len(narrow), len(tabs), "narrow strip must drop trailing tabs from hitbox list")
	for _, h := range narrow {
		assert.LessOrEqual(t, h.X1, 30, "every visible hitbox must end at or before the cutoff width")
	}
}

func TestWorkspaceTabHitboxes_EmptyInput(t *testing.T) {
	assert.Nil(t, WorkspaceTabHitboxes(nil, WSTabInspector, 80))
	assert.Nil(t, WorkspaceTabHitboxes([]WorkspaceTab{}, WSTabInspector, 80))
}

// TestRenderWorkspaceTabStrip_DegradesGracefully covers the three width
// regimes: fits exactly, drops trailing tabs with "…", and clips a
// single tab when even that won't fit.
func TestRenderWorkspaceTabStrip_DegradesGracefully(t *testing.T) {
	th := theme.Default()
	tabs := []WorkspaceTab{WSTabInspector, WSTabLogs, WSTabEvents, WSTabYaml}

	wide := RenderWorkspaceTabStrip(th, tabs, WSTabLogs, 200)
	for _, tab := range tabs {
		assert.Contains(t, ansi.Strip(wide), tab.Label(), "wide strip must include every label")
	}

	narrow := RenderWorkspaceTabStrip(th, tabs, WSTabInspector, 25)
	assert.Contains(t, narrow, "…", "overflowing strip must append the ellipsis tail")

	tiny := RenderWorkspaceTabStrip(th, tabs, WSTabYaml, 4)
	assert.NotEmpty(t, tiny, "tiny strip should still render the active label, clipped")
}

func TestRenderWorkspaceTabStrip_Empty(t *testing.T) {
	assert.Empty(t, RenderWorkspaceTabStrip(theme.Default(), nil, WSTabInspector, 40))
}

// TestWorkspaceBodyHeight_FloorsAtOne pins the chrome reservation (4
// rows: panel borders + tab strip + hairline) and the minimum-of-1
// guard. A regression that returned 0 or negative would crash any
// downstream lipgloss layout.
func TestWorkspaceBodyHeight_FloorsAtOne(t *testing.T) {
	assert.Equal(t, 1, WorkspaceBodyHeight(0), "h=0 must floor at 1, not return 0 or negative")
	assert.Equal(t, 1, WorkspaceBodyHeight(4), "h=4 (chrome budget) must floor at 1")
	assert.Equal(t, 6, WorkspaceBodyHeight(10))
	assert.Equal(t, 36, WorkspaceBodyHeight(40))
}

// TestWorkspaceTab_LabelsHaveHotkeyHint pins the convention: every
// non-Inspector tab embeds its hotkey in parens (e.g. "(l)ogs") so
// operators can scan the strip without consulting `?`. The Inspector
// fallback is its number, which we accept; an unrecognised tab returns
// the empty string and we keep that as the explicit signal.
func TestWorkspaceTab_LabelsHaveHotkeyHint(t *testing.T) {
	for _, tab := range []WorkspaceTab{WSTabInspector, WSTabLogs, WSTabEvents, WSTabYaml, WSTabChat, WSTabHot, WSTabForwards} {
		label := tab.Label()
		assert.NotEmpty(t, label, "tab %d must have a label", tab)
		assert.GreaterOrEqual(t, len(strings.Fields(label)), 2,
			"tab %v label %q must separate the hotkey from its name", tab, label)
	}
	assert.Equal(t, "Drain", WSTabDrain.Label(), "an operation with no activation key must not advertise one")
	assert.Empty(t, WorkspaceTab(99).Label(), "unknown tab values must return empty string (explicit signal)")
}
