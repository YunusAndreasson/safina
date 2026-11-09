package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
)

func clusterItems(names ...string) []config.ContextInfo {
	out := make([]config.ContextInfo, len(names))
	for i, n := range names {
		out[i] = config.ContextInfo{Name: n}
	}
	return out
}

// TestClusterSwitcher_OpenPositionsCursorOnActive pins the documented
// behaviour: Open() seeds the cursor on the active context so the operator
// sees "you are here" without scrolling. The "no default cluster" feedback
// rule applies at *startup* only — mid-session this dot is correct.
func TestClusterSwitcher_OpenPositionsCursorOnActive(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("alpha", "beta", "gamma")
	s.Open(items, "beta")

	sel, ok := s.Selected()
	require.True(t, ok)
	assert.Equal(t, "beta", sel.Name)
}

func TestClusterSwitcher_OpenWithoutMatchKeepsCursorAtZero(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("alpha", "beta")
	s.Open(items, "not-in-list")

	sel, ok := s.Selected()
	require.True(t, ok)
	assert.Equal(t, "alpha", sel.Name)
}

func TestClusterSwitcher_SelectedEmptyList(t *testing.T) {
	s := NewClusterSwitcher()
	s.Open(nil, "")
	_, ok := s.Selected()
	assert.False(t, ok, "empty corpus must report no selection")
}

// TestClusterSwitcher_FuzzyFilterFindsByGap covers the fuzzy.Find path —
// the matcher accepts gaps, so "ag" must surface "alpha-gamma" even when
// no contiguous "ag" substring exists.
func TestClusterSwitcher_FuzzyFilterFindsByGap(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("alpha", "beta", "alpha-gamma", "delta")
	s.Open(items, "alpha")

	s.SetQuery("ag")
	sel, ok := s.Selected()
	require.True(t, ok, "fuzzy filter must match alpha-gamma via gap")
	assert.Equal(t, "alpha-gamma", sel.Name)
}

// TestClusterSwitcher_SetQueryResetsCursorToZero pins the documented
// reset: after typing, the cursor jumps to the first match so the user's
// next Enter selects what's visible at the top — not a stale position
// from before filtering.
func TestClusterSwitcher_SetQueryResetsCursorToZero(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("alpha", "beta", "gamma")
	s.Open(items, "gamma") // cursor lands on index 2
	require.Equal(t, 2, s.cursor)

	s.SetQuery("a") // matches alpha, gamma — cursor must reset
	require.Equal(t, 0, s.cursor)
	sel, ok := s.Selected()
	require.True(t, ok)
	assert.Equal(t, "alpha", sel.Name, "first match must be selected after SetQuery")
}

// TestClusterSwitcher_CursorClampedAtEdges defends against off-by-one
// keypresses (j on the last row, k on the first row) producing invalid
// indices into the matches slice.
func TestClusterSwitcher_CursorClampedAtEdges(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("a", "b", "c")
	s.Open(items, "a")

	s.CursorUp() // already at top
	assert.Equal(t, 0, s.cursor)

	s.CursorDown()
	s.CursorDown()
	assert.Equal(t, 2, s.cursor)
	s.CursorDown() // past bottom
	assert.Equal(t, 2, s.cursor, "CursorDown must clamp at last index, not wrap or overrun")
}

// TestClusterSwitcher_QueryNarrowsCursorBounds pins the invariant that
// after filtering, CursorDown clamps against the *filtered* count, not
// the full corpus size — otherwise Enter on a synthetic past-the-end
// index would crash Selected.
func TestClusterSwitcher_QueryNarrowsCursorBounds(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("alpha", "beta", "gamma", "delta")
	s.Open(items, "alpha")

	s.SetQuery("a") // alpha, gamma, delta
	for range 10 {
		s.CursorDown()
	}
	sel, ok := s.Selected()
	require.True(t, ok, "Selected must remain valid after spamming CursorDown")
	assert.Contains(t, []string{"alpha", "gamma", "delta"}, sel.Name)
}

func TestClusterSwitcher_QueryRoundTrips(t *testing.T) {
	s := NewClusterSwitcher()
	s.SetQuery("hello")
	assert.Equal(t, "hello", s.Query())
}

// Typing part of a name means that name. Fuzzy scores alone ranked a long
// GKE-style context (e…k…s across separators) above the EKS one for "eks",
// so Enter opened the wrong cluster.
func TestClusterSwitcher_LiteralMatchOutranksScatteredOne(t *testing.T) {
	s := NewClusterSwitcher()
	items := clusterItems("gke_acme-services-000000_asia-south1-b_gke-shop", "lab", "deploy_bot@eks-main.eu-north-1.eksctl.io")
	s.Open(items, "lab")

	s.SetQuery("eks")
	sel, ok := s.Selected()
	require.True(t, ok)
	assert.Equal(t, "deploy_bot@eks-main.eu-north-1.eksctl.io", sel.Name)
	require.Len(t, s.matches, 2, "scattered matches still follow")
}
