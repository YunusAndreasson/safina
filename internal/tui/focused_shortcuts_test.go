package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
)

func TestTreeFilterGetsLiteralYWhileReadingAnotherView(t *testing.T) {
	for _, st := range []state{stateLogView, stateEventsView, stateDescribeView, stateMetricsView, stateChatView} {
		m := shiftFooterModel(t)
		m.logs.Append("existing log payload")
		m.describe.SetYAML("kind: Pod\nmetadata:\n  name: existing", nil)
		m.events.SetItems([]k8s.EventInfo{{Message: "existing event"}}, nil)
		m.state = st
		m.treeFocusInImmersive = true
		m.tree.Focused = true
		m.handleKey(keyMsg("/"))
		require.True(t, m.tree.FilterActive())
		m.handleKey(keyMsg("y"))
		require.Equal(t, "y", m.tree.FilterValue(), "view %v must not steal a tree-filter character", st)
		require.NotEqual(t, "copied", m.bannerMsg)
	}
}
