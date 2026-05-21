package sections

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// stripANSI is defined in logs_highlight_test.go alongside the log-renderer
// fixtures; reuse it via the shared package test scope.

// kindRowFixture is the boilerplate every per-kind row test needs: a fresh
// Tree wired to a real-ish Ctx (theme/keys, no manager) so the row renderers
// can read styles + paint cells without spinning up a k8s.Manager.
type kindRowFixture struct {
	tr  Tree
	ctx *tuictx.Ctx
}

func newKindRowFixture(t *testing.T) kindRowFixture {
	t.Helper()
	ctx := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	return kindRowFixture{tr: NewTree(ctx), ctx: ctx}
}

func TestTree_ServiceRows_ShowPortsColumn(t *testing.T) {
	f := newKindRowFixture(t)
	f.tr.children = &Children{
		Kind: k8s.KindService,
		Services: []k8s.ServiceInfo{
			{Name: "api", Namespace: "shop", Type: "ClusterIP", ClusterIP: "10.0.0.1", Ports: "80/TCP", Age: "5d"},
		},
	}

	out := f.tr.renderServiceRows(f.ctx, 120, 5)

	assert.Contains(t, out, "PORTS", "wide layout must include the PORTS header")
	assert.Contains(t, out, "80/TCP", "row must render the ports value")
}

func TestTree_ServiceRows_PortsColumnTruncatesGracefully(t *testing.T) {
	f := newKindRowFixture(t)
	long := "80/TCP,443/TCP,8080/TCP,8081/TCP,9090/TCP"
	f.tr.children = &Children{
		Kind: k8s.KindService,
		Services: []k8s.ServiceInfo{
			{Name: "api", Namespace: "shop", Type: "ClusterIP", ClusterIP: "10.0.0.1", Ports: long, Age: "5d"},
		},
	}

	out := f.tr.renderServiceRows(f.ctx, 80, 5)

	// The full string never fits a 14-char PORTS column; trunc-pad uses an
	// ellipsis. The visible prefix must come from the original value.
	assert.Contains(t, out, "80/TCP", "truncated cell still starts with the original prefix")
	assert.NotContains(t, out, long, "long port lists must be truncated, not overflow")
}

func TestTree_ServiceRows_DropPortsUnderNarrowWidth(t *testing.T) {
	f := newKindRowFixture(t)
	f.tr.children = &Children{
		Kind: k8s.KindService,
		Services: []k8s.ServiceInfo{
			{Name: "api", Type: "ClusterIP", ClusterIP: "10.0.0.1", Ports: "80/TCP", Age: "5d"},
		},
	}

	out := f.tr.renderServiceRows(f.ctx, 40, 5)

	// PORTS is the first thing to drop under narrow widths so name + IP stay readable.
	assert.NotContains(t, out, "PORTS", "narrow layout must drop the PORTS column")
	assert.Contains(t, out, "CLUSTER-IP")
}

func TestTree_IngressRows_HOSTSColumnHidesWhenAllEmpty(t *testing.T) {
	t.Run("no hosts hides column", func(t *testing.T) {
		f := newKindRowFixture(t)
		f.tr.children = &Children{
			Kind: k8s.KindIngress,
			Ingresses: []k8s.IngressInfo{
				{Name: "x", Class: "nginx", Age: "1d"},
			},
		}
		out := f.tr.renderIngressRows(f.ctx, 120, 5)
		assert.NotContains(t, out, "HOSTS", "no ingress has hosts → HOSTS column hidden")
	})
	t.Run("any host shows column", func(t *testing.T) {
		f := newKindRowFixture(t)
		f.tr.children = &Children{
			Kind: k8s.KindIngress,
			Ingresses: []k8s.IngressInfo{
				{Name: "x", Class: "nginx", Age: "1d"},
				{Name: "y", Class: "nginx", Hosts: "foo.example.com", Age: "1d"},
			},
		}
		out := f.tr.renderIngressRows(f.ctx, 120, 5)
		assert.Contains(t, out, "HOSTS", "any ingress with hosts → HOSTS column visible")
		assert.Contains(t, out, "foo.example.com")
	})
}

func TestTree_PVCRows_ShowVolumeWhenBound(t *testing.T) {
	t.Run("bound shows volume", func(t *testing.T) {
		f := newKindRowFixture(t)
		f.tr.children = &Children{
			Kind: k8s.KindPVC,
			PVCs: []k8s.PVCInfo{
				{Name: "data-0", Namespace: "shop", Status: "Bound", Capacity: "10Gi", VolumeName: "pvc-abc-123", StorageClass: "fast", Age: "5d"},
			},
		}
		out := f.tr.renderPVCRows(f.ctx, 120, 5)
		assert.Contains(t, out, "VOLUME")
		assert.Contains(t, out, "pvc-abc-123")
	})
	t.Run("all pending hides volume", func(t *testing.T) {
		f := newKindRowFixture(t)
		f.tr.children = &Children{
			Kind: k8s.KindPVC,
			PVCs: []k8s.PVCInfo{
				{Name: "data-pending", Namespace: "shop", Status: "Pending", StorageClass: "fast", Age: "1m"},
			},
		}
		out := f.tr.renderPVCRows(f.ctx, 120, 5)
		assert.NotContains(t, out, "VOLUME", "VolumeName empty across all PVCs → VOLUME column hidden")
	})
}

func TestTree_PVRows_ShowClaimAccessReclaim(t *testing.T) {
	f := newKindRowFixture(t)
	f.tr.children = &Children{
		Kind: k8s.KindPV,
		PVs: []k8s.PVInfo{
			{Name: "pv-001", Capacity: "10Gi", Status: "Bound", Claim: "shop/data-0", StorageClass: "fast", AccessModes: "RWO", Reclaim: "Retain", Age: "5d"},
		},
	}

	out := f.tr.renderPVRows(f.ctx, 200, 5)

	// All three optional columns must render together at a wide width.
	for _, want := range []string{"CLAIM", "ACCESS", "RECLAIM"} {
		assert.Contains(t, out, want, "expected %q header at wide width", want)
	}
	assert.Contains(t, out, "shop/data-0")
	assert.Contains(t, out, "RWO")
	assert.Contains(t, out, "Retain")

	// Column order: STATUS < CLAIM < CAPACITY < ACCESS < RECLAIM < AGE.
	// lipgloss may wrap a styled header across physical lines (header cells
	// are emitted as fixed-width style blocks; if any cell rolls past the
	// terminal column the join inserts a newline). Strip ANSI and flatten
	// newlines so we can compare positions of labels regardless of where
	// the wrap fell. CAPACITY's column is 7 wide so the visible label is
	// the truncated prefix "CAPACIT" — anchor on the prefix.
	flat := strings.ReplaceAll(stripANSI(out), "\n", " ")
	// Trim to the first data row so trailing rows can't be the source of an
	// earlier match. The first data cell is the pv-001 name.
	if cut := strings.Index(flat, "pv-001"); cut > 0 {
		flat = flat[:cut]
	}
	idx := func(s string) int { return strings.Index(flat, s) }
	require.Greater(t, idx("CLAIM"), idx("STATUS"), "CLAIM sits to the right of STATUS")
	require.Greater(t, idx("CAPACI"), idx("CLAIM"), "CAPACITY sits to the right of CLAIM")
	require.Greater(t, idx("ACCESS"), idx("CAPACI"), "ACCESS sits to the right of CAPACITY")
	require.Greater(t, idx("RECLAIM"), idx("ACCESS"), "RECLAIM sits to the right of ACCESS")
	require.Greater(t, idx("AGE"), idx("RECLAIM"), "AGE sits to the right of RECLAIM")
}

func TestTree_PVRows_DropPriorityUnderNarrowWidth(t *testing.T) {
	mkTree := func() *Tree {
		f := newKindRowFixture(t)
		f.tr.children = &Children{
			Kind: k8s.KindPV,
			PVs: []k8s.PVInfo{
				{Name: "pv-001", Capacity: "10Gi", Status: "Bound", Claim: "shop/data-0", StorageClass: "fast", AccessModes: "RWO", Reclaim: "Retain", Age: "5d"},
			},
		}
		tr := f.tr
		return &tr
	}

	// At each step the column dropped should follow the documented priority
	// (RECLAIM first, then ACCESS, then CLAIM). Exact width thresholds depend
	// on EdgeTotal/GapTotal; this test brackets behaviour by checking that as
	// width shrinks, columns disappear in the expected order rather than
	// asserting on the exact pixel where each transition happens.
	wide := mkTree().renderPVRows(newKindRowFixture(t).ctx, 200, 5)
	mid := mkTree().renderPVRows(newKindRowFixture(t).ctx, 70, 5)
	narrow := mkTree().renderPVRows(newKindRowFixture(t).ctx, 50, 5)

	assert.Contains(t, wide, "RECLAIM", "wide layout keeps RECLAIM")
	assert.Contains(t, wide, "ACCESS", "wide layout keeps ACCESS")
	assert.Contains(t, wide, "CLAIM", "wide layout keeps CLAIM")

	// If a column was dropped at width 70, RECLAIM is the one most likely to go.
	if !strings.Contains(mid, "RECLAIM") || !strings.Contains(mid, "ACCESS") || !strings.Contains(mid, "CLAIM") {
		// Once RECLAIM is dropped, ACCESS shouldn't disappear before it; once ACCESS is dropped, CLAIM shouldn't disappear before it.
		if !strings.Contains(mid, "ACCESS") {
			assert.NotContains(t, mid, "RECLAIM", "ACCESS dropped before RECLAIM violates priority")
		}
		if !strings.Contains(mid, "CLAIM") {
			assert.NotContains(t, mid, "ACCESS", "CLAIM dropped before ACCESS violates priority")
		}
	}

	// Narrow always drops at least RECLAIM (its width budget can't fit all three).
	assert.NotContains(t, narrow, "RECLAIM", "narrowest layout must drop RECLAIM first")

	// Name column never collapses below 10. We can't read the cell width directly,
	// but the row should still contain the name — TruncPad guarantees a >=10 cell.
	assert.Contains(t, narrow, "pv-001")
}
