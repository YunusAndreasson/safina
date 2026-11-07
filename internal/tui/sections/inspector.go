package sections

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Inspector is the ambient detail tile at the top of the right stack.
// It renders a curated, kind-aware view of whatever the tree cursor is
// on, deliberately *avoiding* the fields the row column already shows
// (namespace, age, ready, live CPU/MEM usage — those are scannable from
// the list). Instead it surfaces the stuff a Get call brings back:
// container images, pod IP, QoS class, configured requests / limits,
// conditions, taints, endpoint counts.
//
// Detail fetches are kicked off on selection change and cached per
// (kind, namespace, name) so cursor movement up/down a list of pods
// stays free of repeated round-trips after the first visit.
type Inspector struct {
	// ref is the last selection we computed a snapshot for. Compared on
	// SetSelection to short-circuit when the cursor hasn't moved.
	ref NodeRef

	// snap is the base-from-list snapshot (kind-aware framing + a few
	// fields we can know without a Get). The detail enrichment merges
	// into this when it arrives so the UI never blanks during a refetch.
	snap *inspectorSnapshot

	// detail caches the deep-fetch results keyed by inspectorDetailKey.
	// Cleared on cluster switch via Clear; per-entry refresh isn't
	// throttled separately here — the cache is invalidated when the
	// detail fetch fires (load issuance is itself throttled by the
	// "only fetch if not cached" gate below).
	detail map[string]*inspectorDetail
}

// inspectorDetail is the kind-erased payload for the detail cache.
// Exactly one field is non-nil, matching the row's kind.
type inspectorDetail struct {
	pod     *k8s.PodDetail
	app     *k8s.AppDetail
	node    *k8s.NodeDetail
	service *k8s.ServiceDetail
	ingress *k8s.IngressDetail
	pvc     *k8s.PVCDetail
	pv      *k8s.PVDetail
	err     error
}

// inspectorSnapshot is the kind-erased bag of rows the View() renders
// verbatim. Each row is "label · value", and a kind-specific builder
// fills it with a mix of "knowable-from-list" and "needs-detail-fetch"
// fields. The renderer is dumb — adding a new kind only touches the
// per-kind builder.
type inspectorSnapshot struct {
	title    string // heading line, e.g. "Pod · checkout-api-xq1z2"
	subtitle string // optional kind / type breadcrumb under the title

	// statusText / statusKind drive the one-line headline above rows:
	// "Healthy · Progressing", "Pending · ImagePullBackOff", etc. We
	// reserve this for state that genuinely doesn't live in any row
	// column the operator just glanced at.
	statusText string
	statusKind string // "good" | "warn" | "bad" | ""

	// sections groups rows visually: "Runtime", "Resources", "Network",
	// "Conditions". Each section renders with a small label so the
	// inspector reads as a structured card, not a flat key/value dump.
	sections []inspectorSection

	// live carries the inline CPU/MEM trend pair. Populated for Pod/App
	// selections that have at least one buffered sample in the tree's
	// metric history; nil otherwise (the renderer skips the block). The
	// buffer pointers alias the tree's rolling store, so the wide
	// sparkline always reads the freshest samples without rebuild.
	live *inspectorLive

	// loading indicates a detail fetch is in flight. The renderer adds
	// a dim spinner suffix to the title so the operator knows
	// enrichment is coming.
	loading bool

	// err carries a detail-fetch failure. We still render the base
	// rows; the error sits at the bottom as a small dim line.
	err error
}

type inspectorSection struct {
	title string // optional; empty hides the header
	rows  []inspectorRow
}

type inspectorRow struct {
	label string
	value string
	// emphasis: "name" → bold-fg, "dim" → dim-fg, "good"/"warn"/"bad"
	// → semantic color. Empty → plain fg.
	emphasis string
}

// inspectorLive bundles the per-selection metric trend rendered as a
// two-row block in the Inspector. cpuBuf / memBuf alias the tree's
// rolling buffers so the sparkline reads live samples on every render.
// curCPU/curMem are the latest list-call readings. Configured budgets
// (requests / limits) deliberately don't live here — they get their own
// "resources (configured)" section in the per-kind builder.
type inspectorLive struct {
	cpuBuf *components.MetricBuffer
	memBuf *components.MetricBuffer
	curCPU int64
	curMem int64
}

// NewInspector constructs an empty inspector.
func NewInspector() Inspector {
	return Inspector{detail: make(map[string]*inspectorDetail)}
}

// Clear drops the snapshot AND the detail cache. Called on cluster
// switch / picker pop so stale detail from another cluster doesn't
// bleed in.
func (i *Inspector) Clear() {
	i.snap = nil
	i.ref = NodeRef{}
	i.detail = make(map[string]*inspectorDetail)
}

// SetSelection re-renders the snapshot for the current tree cursor and
// returns an optional tea.Cmd to fetch the kind-specific detail when
// it's not already cached. Returns nil when the row doesn't have a
// deep-fetch path (ConfigMap / Secret / PVC / PV — the row already
// tells you everything).
//
// Always re-renders the base snapshot — that way a tree auto-refresh
// (same selection, new row data) updates the headline + identity bits
// without waiting for the detail call to round-trip. The detail fetch
// only fires when the selection *changed* and we don't have a cached
// answer; same-selection re-syncs reuse whatever's already in the cache.
func (i *Inspector) SetSelection(ctx context.Context, mgr *k8s.Manager, contextName string, tr *Tree) tea.Cmd {
	ref := tr.SelectedRef()
	if ref.Kind == "" {
		i.snap = nil
		i.ref = ref
		return nil
	}
	sameRef := i.ref == ref
	i.ref = ref

	key := inspectorDetailKey(ref.Kind, ref.Namespace, ref.Name)
	cached := i.detail[key]
	i.snap = i.buildSnapshot(tr, ref, cached)

	if sameRef || !kindSupportsDetail(ref.Kind) || mgr == nil || contextName == "" {
		return nil
	}
	if cached != nil {
		return nil
	}
	i.snap.loading = true
	return LoadInspectorDetail(ctx, mgr, contextName, ref.Kind, ref.SubKind, ref.Namespace, ref.Name)
}

// Apply ingests a detail-load result, caches it, and re-renders the
// snapshot if the result is still for the current selection. Stale
// results (the user navigated away mid-fetch) are kept in the cache
// for the future, but don't disturb the live render.
func (i *Inspector) Apply(msg InspectorDetailMsg, tr *Tree) {
	key := inspectorDetailKey(msg.Kind, msg.Namespace, msg.Name)
	det := &inspectorDetail{
		pod:     msg.Pod,
		app:     msg.App,
		node:    msg.Node,
		service: msg.Service,
		ingress: msg.Ingress,
		pvc:     msg.PVC,
		pv:      msg.PV,
		err:     msg.Err,
	}
	i.detail[key] = det
	cur := i.ref
	if cur.Kind == msg.Kind && cur.Namespace == msg.Namespace && cur.Name == msg.Name {
		i.snap = i.buildSnapshot(tr, cur, det)
	}
}

// View renders the tile.
func (i *Inspector) View(c *tuictx.Ctx, w, h int) string {
	t := c.Theme
	if i.snap == nil {
		body := t.Idle.Render("nothing selected")
		return components.PanelWithAccent(t, "Inspector", w, h, body, false, t.P.Dim, t.P.Dim)
	}
	body := i.renderBody(c, components.PanelContentWidth(w), h-2)
	title := "Inspector"
	if i.snap.loading {
		title = "Inspector · " + c.Spinner.View() + " loading…"
	}
	return components.PanelWithAccent(t, title, w, h, body, false, t.P.Cyan, t.P.Dim)
}

func (i *Inspector) renderBody(c *tuictx.Ctx, innerW, innerH int) string {
	t := c.Theme
	s := i.snap

	// Identity group: title + subtitle + status form a single visual unit —
	// no blank lines between them. The typographic register (bold name,
	// dim subtitle, colored status) carries the Gestalt grouping; vertical
	// gaps would just push the eye to read them as three separate things.
	parts := []string{
		t.NameStrong.Render(truncate(s.title, innerW)),
	}
	if s.subtitle != "" {
		parts = append(parts, t.DimTxt.Render(truncate(s.subtitle, innerW)))
	}
	if s.statusText != "" {
		parts = append(parts, statusLine(t, s.statusText, s.statusKind, innerW))
	}

	// Live trends sit immediately under the identity strip — same visual
	// unit as "what is this thing right now." The cpu/mem row labels are
	// self-identifying; an explicit "LIVE USAGE" header just adds noise and
	// breaks proximity between status and trend.
	if s.live != nil {
		parts = append(parts, renderLiveMetric(t, "cpu", s.live, true, innerW))
		parts = append(parts, renderLiveMetric(t, "mem", s.live, false, innerW))
	}

	// Global label width — every detail row across every section right-pads
	// its label to the same cell count, creating a strong vertical edge
	// where the "value column" begins. Without this, each section computed
	// its own widestLabel and the value column zigzagged section-to-section.
	labelW := globalInspectorLabelW(s.sections)

	// Sections are appended whole — if one wouldn't fit (divider + at least
	// one row) we skip it entirely. Truncating mid-section leaves a lonely
	// header line that reads as a render bug rather than progressive
	// disclosure. We reserve room at the tail for the optional error line
	// so an enrichment failure always has space to surface.
	errReserve := 0
	if s.err != nil {
		errReserve = 2 // blank + error line
	}
	// Sections are separated only by a blank line — Gestalt proximity does
	// the grouping work. Headers like "rollout" / "images" / "runtime" are
	// redundant when the rows are self-labeling (`replicas`, `image`,
	// `pod IP` already tell the eye what group they belong to), and a
	// dim rule on every section adds chrome that fights with the data.
	// Whole-or-nothing: skip a section that can't fit at least one row,
	// so the tail never lands on a stub.
	for _, sec := range s.sections {
		if len(sec.rows) == 0 {
			continue
		}
		need := 1 + len(sec.rows) // blank + rows
		minNeed := 1 + 1          // blank + at least 1 row
		remaining := innerH - len(parts) - errReserve
		if remaining < minNeed {
			break
		}
		parts = append(parts, "")
		maxRows := len(sec.rows)
		if need > remaining {
			maxRows = remaining - 1
		}
		for i := range maxRows {
			parts = append(parts, renderInspectorRow(t, sec.rows[i], labelW, innerW))
		}
		if maxRows < len(sec.rows) {
			break
		}
	}

	if s.err != nil {
		parts = append(parts, "")
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Red).
			Render(truncate("✗ "+s.err.Error(), innerW)))
	}

	if len(parts) > innerH {
		parts = parts[:innerH]
	}
	return strings.Join(parts, "\n")
}

// globalInspectorLabelW returns the widest label across every section's
// rows, so every value column starts at the same cell. Caps at 12 to
// keep the value column from being pushed too far right on narrow panes
// — same cap widestLabel used for the per-section calculation.
func globalInspectorLabelW(secs []inspectorSection) int {
	w := 0
	for _, sec := range secs {
		for _, r := range sec.rows {
			if lw := lipgloss.Width(r.label); lw > w {
				w = lw
			}
		}
	}
	if w > 12 {
		w = 12
	}
	return w
}

// emphasisColor maps the inspector's emphasis tags ("good"/"warn"/"bad"/
// "name"/"dim") to a palette color. Single source of truth so the headline
// status, per-row values, and any future emphasis sites stay in lockstep.
func emphasisColor(t *theme.Theme, kind string) color.Color {
	switch kind {
	case "name":
		return t.P.FG
	case "dim":
		return t.P.Dim
	case "good":
		return t.P.Green
	case "warn":
		return t.P.Yellow
	case "bad":
		return t.P.Red
	}
	return t.P.FGFade
}

// statusLine paints the headline status under the identity strip. Color
// hierarchy: the saturated colors and bold weight are reserved for warn /
// bad — the states that *demand* eye-fall. "good" steady states are
// rendered dim italic, mirroring the same affirmation-suppression
// principle used for "Running" in the status glyph (the resource is fine;
// shouting that fact every refresh just trains the eye to ignore the line).
func statusLine(t *theme.Theme, text, kind string, innerW int) string {
	text = truncate(text, innerW)
	if kind == "good" {
		return t.Hint.Render(text)
	}
	return lipgloss.NewStyle().Foreground(emphasisColor(t, kind)).Bold(true).Render(text)
}

// liveValueCellW is the joint width for the current-value sub-column —
// sized for the worst typical reading across both kinds ("9999m" for
// CPU, "1024Mi" for memory), so the cpu and mem rows share a single
// value-edge instead of misaligning by one cell.
const liveValueCellW = 6

// renderLiveMetric paints one row of the LIVE USAGE block: label, wide
// history sparkline, current value. The configured req/limit budgets
// (and node request-sum / allocatable) live in their own "resources"
// section below, so the live row is just trend + reading — no
// annotation. The sparkline width is derived from a constant right
// block (= liveValueCellW), keeping the spark edge invariant across:
//
//   - refreshes (value digit changes don't shift it)
//   - rows (cpu ↔ mem stay column-aligned)
//
// The trend window adapts to retained history via SparklineFull's
// bucket-aggregation: a fresh session shows minutes; one left running
// shows hours.
func renderLiveMetric(t *theme.Theme, kind string, live *inspectorLive, isCPU bool, innerW int) string {
	const labelW = 4 // "cpu " / "mem "
	const gap = 1
	label := t.DimTxt.Width(labelW).Render(kind)

	var buf *components.MetricBuffer
	var cur int64
	var curStr string
	if isCPU {
		buf, cur = live.cpuBuf, live.curCPU
		curStr = k8s.FormatCPU(cur)
	} else {
		buf, cur = live.memBuf, live.curMem
		curStr = k8s.FormatMem(cur)
	}

	// Right-pad the value into a fixed cell so cpu↔mem rows share an
	// edge regardless of digit count.
	valPad := liveValueCellW - lipgloss.Width(curStr)
	if valPad < 0 {
		valPad = 0
	}
	valStyled := lipgloss.NewStyle().Foreground(t.P.FG).Render(curStr)
	valSlot := strings.Repeat(" ", valPad) + valStyled

	sparkW := innerW - labelW - gap - liveValueCellW - gap
	if sparkW < 8 {
		sparkW = 8
	}
	spark := components.SparklineFull(t, buf, sparkW, lipgloss.NewStyle())

	return label + spark + strings.Repeat(" ", gap) + valSlot
}

// attachPodLive populates the LIVE USAGE block for a pod selection.
// Pulls the live sparkline buffer from the tree (shared pointer — keeps
// updating on subsequent refreshes) and the configured requests/limits
// from the detail fetch when it has landed. No-op when no samples yet.
//
// Semantics: curCPU/curMem are the instantaneous metrics-server reading
// captured at the most recent refresh tick (5s cadence). NOT cumulative;
// a steady-state pod renders a flat value, an application accumulating
// page cache or leaking memory renders a climbing value — both faithful
// to what metrics-server reports. The displayed number and the
// rightmost sparkline sample are derived from the same a.Memory /
// p.Memory field, so they never disagree.
func attachPodLive(s *inspectorSnapshot, tr *Tree, p k8s.PodInfo, _ *inspectorDetail) {
	cpuBuf, memBuf := tr.MetricSeriesFor(k8s.KindPod, p.Namespace, p.Name)
	live := buildLive(cpuBuf, memBuf, p.CPU, p.Memory)
	if live == nil {
		return
	}
	s.live = live
}

// attachAppLive populates the LIVE USAGE block for an App selection. The
// row's CPU/Memory are a sum across the workload's selector-matching
// pods (see aggregateForSelector); during a rolling update both old
// and new pods may be alive simultaneously and the sum briefly climbs
// then settles — that is correct behaviour, not double-counting. The
// configured per-pod request/limit (when the detail fetch returned)
// is reported next to the current value so the operator reads "we're
// at X, per-replica budget is Y" without leaving the panel.
func attachAppLive(s *inspectorSnapshot, tr *Tree, a k8s.AppInfo, _ *inspectorDetail) {
	cpuBuf, memBuf := tr.MetricSeriesFor(k8s.KindApp, a.Namespace, a.Name)
	live := buildLive(cpuBuf, memBuf, a.CPU, a.Memory)
	if live == nil {
		return
	}
	s.live = live
}

// buildLive is the shared constructor for the inspectorLive block. Returns
// nil when no metric history exists for the selection (both buffers
// nil) — caller treats that as "skip the live block this frame". CPU /
// memory values flow in as int64 (millicores, bytes) and round-trip to
// the renderer unchanged; formatting happens at renderLiveMetric so any
// change to the unit display is in one place.
func buildLive(cpuBuf, memBuf *components.MetricBuffer, curCPU, curMem int64) *inspectorLive {
	if cpuBuf == nil && memBuf == nil {
		return nil
	}
	return &inspectorLive{
		cpuBuf: cpuBuf,
		memBuf: memBuf,
		curCPU: curCPU,
		curMem: curMem,
	}
}

// attachNodeLive populates the LIVE USAGE block for a node selection.
// Same shape Apps and Pods use, with one semantic adjustment: the
// sparkline buffer for nodes holds per-mille utilisation (0-1000
// mapping to 0-100% of allocatable; ratchets to 2000 on overcommit)
// rather than raw values. The gradient colours therefore align with
// the percentage the row shows, while the headline value next to the
// spark is the *raw* CPU / Memory usage so the operator reads
// "1.5 cores in use" without doing arithmetic against the budget.
func attachNodeLive(s *inspectorSnapshot, tr *Tree, n k8s.NodeInfo) {
	cpuBuf, memBuf := tr.MetricSeriesFor(k8s.KindNode, "", n.Name)
	live := buildLive(cpuBuf, memBuf, n.CPUUsed, n.MemUsed)
	if live == nil {
		return
	}
	s.live = live
}

func renderInspectorRow(t *theme.Theme, r inspectorRow, labelW, innerW int) string {
	label := lipgloss.NewStyle().Foreground(t.P.Dim).Width(labelW).Render(r.label)
	valueW := innerW - labelW - 2
	if valueW < 4 {
		valueW = 4
	}
	valStyle := lipgloss.NewStyle().Foreground(emphasisColor(t, r.emphasis))
	// Bold is reserved for outlier states only. "name" emphasis appears 30+
	// times across a typical Pod/Deployment inspector — making each one bold
	// trains the eye to ignore the weight, defeating its purpose. The FG vs
	// Dim contrast against the label column already distinguishes "value"
	// from "label"; bold is held in reserve for warn/bad rows.
	switch r.emphasis {
	case "warn", "bad":
		valStyle = valStyle.Bold(true)
	}
	// Plain default needs a real FG (emphasisColor returns FGFade for unknown
	// kinds, which is the headline default but too soft for row values).
	if r.emphasis == "" {
		valStyle = valStyle.Foreground(t.P.FG)
	}
	return label + "  " + valStyle.Render(truncate(r.value, valueW))
}

// ── snapshot builders ────────────────────────────────────────────────

// kindSupportsDetail reports whether a kind has a registered deep-fetch
// path. ConfigMap / Secret / PVC / PV / NodePool / Namespace stay false
// — the row already tells you everything that matters at the inspector
// scale.
func kindSupportsDetail(kind string) bool {
	switch kind {
	case k8s.KindPod, k8s.KindApp, k8s.KindNode, k8s.KindService, k8s.KindIngress,
		k8s.KindPVC, k8s.KindPV:
		return true
	}
	return false
}

func (i *Inspector) buildSnapshot(tr *Tree, ref NodeRef, det *inspectorDetail) *inspectorSnapshot {
	switch ref.Kind {
	case k8s.KindApp:
		if a, ok := tr.SelectedAppInfo(); ok {
			snap := inspectAppSnap(a, det)
			attachAppLive(snap, tr, a, det)
			return snap
		}
	case k8s.KindPod:
		if p, ok := tr.SelectedPodInfo(); ok {
			snap := inspectPodSnap(p, det)
			attachPodLive(snap, tr, p, det)
			return snap
		}
	case k8s.KindNode:
		if n, ok := tr.SelectedNodeInfo(); ok {
			snap := inspectNodeSnap(n, det)
			attachNodeLive(snap, tr, n)
			return snap
		}
	case k8s.KindContainer:
		if c, ok := selectedContainer(tr); ok {
			return inspectContainerSnap(c)
		}
	case k8s.KindService:
		if s, ok := selectedService(tr); ok {
			return inspectServiceSnap(s, det)
		}
	case k8s.KindIngress:
		if x, ok := selectedIngress(tr); ok {
			return inspectIngressSnap(x, det)
		}
	case k8s.KindConfigMap:
		if c, ok := selectedConfigMap(tr); ok {
			return inspectConfigMapSnap(c)
		}
	case k8s.KindSecret:
		if s, ok := selectedSecret(tr); ok {
			return inspectSecretSnap(s)
		}
	case k8s.KindPVC:
		if p, ok := selectedPVC(tr); ok {
			return inspectPVCSnap(p, det)
		}
	case k8s.KindPV:
		if p, ok := selectedPV(tr); ok {
			return inspectPVSnap(p, det)
		}
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return &inspectorSnapshot{
			title:    "Namespace · " + ref.Display,
			subtitle: "press ↵ to drill into resources",
		}
	case k8s.KindNodePool:
		if p, ok := selectedNodePool(tr); ok {
			return inspectNodePoolSnap(p)
		}
	case k8s.KindContext:
		return &inspectorSnapshot{
			title:    "Cluster · " + ref.Display,
			subtitle: "press ↵ to drill in",
		}
	}
	if isResourceGroup(ref.Kind) {
		return &inspectorSnapshot{title: selectionTitle(ref), subtitle: "press ↵ to browse resources"}
	}
	return &inspectorSnapshot{title: selectionTitle(ref)}
}

// ── per-kind builders ───────────────────────────────────────────────
//
// Each builder takes the list-call snapshot (what the row already shows)
// plus the optional deep-fetch detail. Rule of thumb: don't emit a row
// for a field that's prominently visible in the tree's row columns. The
// inspector's job is the stuff *behind* the row, not a magnified copy.

func inspectPodSnap(p k8s.PodInfo, det *inspectorDetail) *inspectorSnapshot {
	s := &inspectorSnapshot{title: "Pod · " + p.Name}
	// Subtitle leans on the breadcrumb / row for ns and age, so we keep
	// it to the node name — frequently the field operators want next.
	if p.NodeName != "" {
		s.subtitle = "on " + p.NodeName
	}
	// Headline state: the row already shows status text, but only when
	// abnormal. Surfacing it here gives the same info reading top-down
	// without scanning the row column.
	s.statusText, s.statusKind = podHeadline(p)

	if det != nil && det.pod != nil {
		d := det.pod
		// Runtime — answers "where on the network is this thing?" and
		// "who scheduled it?" — neither is in the row.
		runtime := []inspectorRow{}
		if d.PodIP != "" {
			runtime = append(runtime, inspectorRow{label: "pod IP", value: d.PodIP, emphasis: "name"})
		}
		if d.HostIP != "" {
			runtime = append(runtime, inspectorRow{label: "host IP", value: d.HostIP, emphasis: "dim"})
		}
		// QoS only when it's a non-default classification — surfacing
		// "BestEffort" trains the eye to ignore the field on the
		// majority of pods that don't set requests. Guaranteed and
		// Burstable are the actionable signals.
		if d.QoS != "" && d.QoS != "BestEffort" {
			runtime = append(runtime, inspectorRow{label: "QoS", value: d.QoS, emphasis: "dim"})
		}
		if d.OwnerName != "" {
			runtime = append(runtime, inspectorRow{
				label: "owner", value: d.OwnerKind + "/" + d.OwnerName, emphasis: "dim",
			})
		}
		if d.SchedulingReason != "" {
			runtime = append(runtime, inspectorRow{
				label: "scheduling", value: d.SchedulingReason, emphasis: "warn",
			})
		}
		if len(runtime) > 0 {
			s.sections = append(s.sections, inspectorSection{title: "runtime", rows: runtime})
		}

		// Resources — configured requests/limits, the sister stat to
		// the row's live usage. Skip the whole section when nothing
		// is configured (BestEffort pod with no requests/limits at
		// all is common and the empty section would just be noise).
		if hasResources(d.CPURequestMilli, d.CPULimitMilli, d.MemRequestBytes, d.MemLimitBytes) {
			s.sections = append(s.sections, inspectorSection{
				title: "resources (configured)",
				rows: []inspectorRow{
					{label: "cpu", value: formatReqLim(d.CPURequestMilli, d.CPULimitMilli, "cpu"), emphasis: "name"},
					{label: "mem", value: formatReqLim(d.MemRequestBytes, d.MemLimitBytes, "mem"), emphasis: "name"},
				},
			})
		}

		// Containers — one row per container with image + state.
		// CrashLoopBackOff jumps out in red; healthy containers
		// render dim so they recede.
		if len(d.Containers) > 0 {
			rows := make([]inspectorRow, 0, len(d.Containers))
			for _, c := range d.Containers {
				rows = append(rows, containerRow(c))
			}
			s.sections = append(s.sections, inspectorSection{title: "containers", rows: rows})
		}
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

func inspectAppSnap(a k8s.AppInfo, det *inspectorDetail) *inspectorSnapshot {
	s := &inspectorSnapshot{title: stringsTitle(a.Kind) + " · " + a.Name}
	s.statusText, s.statusKind = appHeadline(a, det)
	if a.HPA != nil {
		hp := a.HPA
		s.subtitle = fmt.Sprintf("HPA %d → %d / %d", hp.Min, hp.Current, hp.Max)
		if hp.PinnedAtMax {
			s.subtitle += " · at max"
		}
	} else if a.NextSchedule != "" {
		s.subtitle = "next run in " + a.NextSchedule
	}

	if det != nil && det.app != nil {
		d := det.app
		rollout := rolloutRows(d)
		if len(rollout) > 0 {
			s.sections = append(s.sections, inspectorSection{title: "rollout", rows: rollout})
		}

		// Images live in their own section so a multi-image
		// pod-template (regular + sidecars) reads cleanly.
		if len(d.Images) > 0 {
			rows := make([]inspectorRow, 0, len(d.Images))
			for n, img := range d.Images {
				label := "image"
				if len(d.Images) > 1 {
					label = fmt.Sprintf("image %d", n+1)
				}
				rows = append(rows, inspectorRow{label: label, value: img, emphasis: "name"})
			}
			s.sections = append(s.sections, inspectorSection{title: "images", rows: rows})
		}

		if hasResources(d.CPURequestMilli, d.CPULimitMilli, d.MemRequestBytes, d.MemLimitBytes) {
			s.sections = append(s.sections, inspectorSection{
				title: "per-replica resources",
				rows: []inspectorRow{
					{label: "cpu", value: formatReqLim(d.CPURequestMilli, d.CPULimitMilli, "cpu"), emphasis: "name"},
					{label: "mem", value: formatReqLim(d.MemRequestBytes, d.MemLimitBytes, "mem"), emphasis: "name"},
				},
			})
		}

		if cond := abnormalConditions(d.Conditions); len(cond) > 0 {
			s.sections = append(s.sections, inspectorSection{title: "conditions", rows: cond})
		}
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

func inspectNodeSnap(n k8s.NodeInfo, det *inspectorDetail) *inspectorSnapshot {
	s := &inspectorSnapshot{title: "Node · " + n.Name}
	if n.Pool != "" {
		s.subtitle = "pool · " + n.Pool
		if n.Roles != "" {
			s.subtitle += "  ·  " + n.Roles
		}
	} else if n.Roles != "" {
		s.subtitle = n.Roles
	}
	s.statusText, s.statusKind = nodeHeadline(n)

	// Capacity sister-stat for the live block above: the sum of pod
	// requests scheduled on this node versus the kubelet's reported
	// allocatable. Reads "scheduler reservation / kubelet allocatable"
	// — answers "is this node oversubscribed by reservations?". Skipped
	// when neither is known (clusters without metrics).
	if hasResources(n.CPURequest, n.CPUAlloc, n.MemRequest, n.MemAlloc) {
		s.sections = append(s.sections, inspectorSection{
			title: "capacity (scheduled / allocatable)",
			rows: []inspectorRow{
				{label: "cpu", value: formatScheduledAllocatable(n.CPURequest, n.CPUAlloc, "cpu"), emphasis: "name"},
				{label: "mem", value: formatScheduledAllocatable(n.MemRequest, n.MemAlloc, "mem"), emphasis: "name"},
			},
		})
	}

	if det != nil && det.node != nil {
		d := det.node
		ver := []inspectorRow{}
		if d.KubeletVersion != "" {
			ver = append(ver, inspectorRow{label: "kubelet", value: d.KubeletVersion, emphasis: "name"})
		}
		if d.ContainerRuntimeVersion != "" {
			ver = append(ver, inspectorRow{label: "runtime", value: d.ContainerRuntimeVersion, emphasis: "dim"})
		}
		if d.OSImage != "" {
			ver = append(ver, inspectorRow{label: "os", value: d.OSImage, emphasis: "dim"})
		}
		if d.KernelVersion != "" {
			ver = append(ver, inspectorRow{label: "kernel", value: d.KernelVersion, emphasis: "dim"})
		}
		if d.Architecture != "" {
			ver = append(ver, inspectorRow{label: "arch", value: d.Architecture, emphasis: "dim"})
		}
		if len(ver) > 0 {
			s.sections = append(s.sections, inspectorSection{title: "versions", rows: ver})
		}

		if len(d.Taints) > 0 {
			rows := make([]inspectorRow, 0, len(d.Taints))
			for _, t := range d.Taints {
				val := t.Key
				if t.Value != "" {
					val += "=" + t.Value
				}
				val += ":" + t.Effect
				rows = append(rows, inspectorRow{label: "taint", value: val, emphasis: "warn"})
			}
			s.sections = append(s.sections, inspectorSection{title: "taints", rows: rows})
		}

		if len(d.Conditions) > 0 {
			rows := make([]inspectorRow, 0, len(d.Conditions))
			for _, c := range d.Conditions {
				rows = append(rows, inspectorRow{
					label: c.Type, value: nonEmpty(c.Reason, "—"), emphasis: "warn",
				})
			}
			s.sections = append(s.sections, inspectorSection{title: "pressure", rows: rows})
		}
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

func inspectServiceSnap(svc k8s.ServiceInfo, det *inspectorDetail) *inspectorSnapshot {
	s := &inspectorSnapshot{title: "Service · " + svc.Name}
	s.subtitle = svc.Type
	if det != nil && det.service != nil {
		d := det.service
		rows := []inspectorRow{}
		if d.HeadlessClusterIP {
			rows = append(rows, inspectorRow{label: "mode", value: "headless (no clusterIP)", emphasis: "dim"})
		}
		if d.ExternalName != "" {
			rows = append(rows, inspectorRow{label: "external", value: d.ExternalName, emphasis: "name"})
		}
		// Ports / cluster IP / type all live in the row's columns — the
		// inspector deliberately skips them. Selector, endpoint health,
		// session affinity, traffic policy, and LB ingress IPs are the
		// fields a row column can't carry.
		if d.Selector != "" {
			rows = append(rows, inspectorRow{label: "selector", value: d.Selector, emphasis: "name"})
		} else {
			rows = append(rows, inspectorRow{label: "selector", value: "(none)", emphasis: "dim"})
		}
		var endpointKind string
		switch {
		case d.EndpointCount == 0:
			endpointKind = "bad"
		case d.ReadyEndpointCount < d.EndpointCount:
			endpointKind = "warn"
		default:
			endpointKind = "good"
		}
		rows = append(rows, inspectorRow{
			label:    "endpoints",
			value:    fmt.Sprintf("%d ready / %d total", d.ReadyEndpointCount, d.EndpointCount),
			emphasis: endpointKind,
		})
		if d.SessionAffinity != "" && d.SessionAffinity != "None" {
			rows = append(rows, inspectorRow{label: "sessionAff", value: d.SessionAffinity, emphasis: "dim"})
		}
		// extPolicy "Cluster" is the default — suppressing it keeps the
		// inspector quiet on the majority of services. "Local" is the
		// noteworthy setting that affects source-IP preservation.
		if d.ExternalTrafficPolicy != "" && d.ExternalTrafficPolicy != "Cluster" {
			rows = append(rows, inspectorRow{label: "extPolicy", value: d.ExternalTrafficPolicy, emphasis: "dim"})
		}
		if len(d.LoadBalancerIngress) > 0 {
			rows = append(rows, inspectorRow{
				label: "lb ingress", value: strings.Join(d.LoadBalancerIngress, ", "), emphasis: "name",
			})
		}
		s.sections = append(s.sections, inspectorSection{rows: rows})
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

func inspectIngressSnap(ing k8s.IngressInfo, det *inspectorDetail) *inspectorSnapshot {
	s := &inspectorSnapshot{title: "Ingress · " + ing.Name}
	if ing.Class != "" {
		s.subtitle = "class · " + ing.Class
	}
	if det != nil && det.ingress != nil {
		d := det.ingress
		// TLS + LB-ingress: a small "exposure" header, separate from
		// the backends list below. Hosts are in the row column, so we
		// don't restate them.
		head := []inspectorRow{}
		if len(d.TLSHosts) > 0 {
			head = append(head, inspectorRow{
				label: "tls hosts", value: strings.Join(d.TLSHosts, ", "), emphasis: "good",
			})
		} else {
			head = append(head, inspectorRow{label: "tls", value: "(none)", emphasis: "dim"})
		}
		if len(d.IngressIP) > 0 {
			head = append(head, inspectorRow{
				label: "lb ingress", value: strings.Join(d.IngressIP, ", "), emphasis: "name",
			})
		}
		s.sections = append(s.sections, inspectorSection{rows: head})

		if len(d.Backends) > 0 {
			s.sections = append(s.sections, inspectorSection{
				title: "backends",
				rows:  backendRows(d.Backends),
			})
		}
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

// backendRows folds the flattened ingress backend list into inspector
// rows: label is "host[/path]" (defaulting to "default" for the catch-all
// backend), value is "service:port". Caps at 5 entries with "+N more"
// so a wildcard ingress doesn't take over the tile.
func backendRows(backends []k8s.IngressBackend) []inspectorRow {
	const limit = 5
	rows := make([]inspectorRow, 0, len(backends))
	for i, b := range backends {
		if i == limit && len(backends) > limit {
			rows = append(rows, inspectorRow{
				label: "more", value: "+" + strconv.Itoa(len(backends)-limit), emphasis: "dim",
			})
			break
		}
		label := b.Host
		if label == "" {
			label = "default"
		}
		if b.Path != "" {
			label += b.Path
		}
		val := "(none)"
		em := "dim"
		if b.Service != "" {
			val = b.Service
			if b.Port != "" {
				val += ":" + b.Port
			}
			em = "name"
		}
		rows = append(rows, inspectorRow{label: label, value: val, emphasis: em})
	}
	return rows
}

// Light-touch builders for kinds that don't get a deep fetch — the row
// already tells the whole story, so the inspector just frames the
// selection so it doesn't render blank.

func inspectContainerSnap(c k8s.ContainerInfo) *inspectorSnapshot {
	s := &inspectorSnapshot{
		title:    "Container · " + c.Name,
		subtitle: "in pod · " + c.PodName,
	}
	if c.State != "" {
		s.statusText = c.State
		s.statusKind = containerStateKind(c.State, c.Ready)
	}
	// Identity: only kind (init / regular / ephemeral) — image already
	// lives in the row's split NAME·IMAGE column.
	id := []inspectorRow{
		{label: "kind", value: c.Kind, emphasis: "dim"},
	}
	if c.LastReason != "" {
		id = append(id, inspectorRow{label: "last", value: c.LastReason, emphasis: "warn"})
	}
	if len(c.Ports) > 0 {
		id = append(id, inspectorRow{
			label: "ports", value: strings.Join(c.Ports, ", "), emphasis: "name",
		})
	}
	s.sections = append(s.sections, inspectorSection{rows: id})

	if hasResources(c.CPURequest, c.CPULimit, c.MemRequest, c.MemLimit) {
		s.sections = append(s.sections, inspectorSection{
			title: "resources (configured)",
			rows: []inspectorRow{
				{label: "cpu", value: formatReqLim(c.CPURequest, c.CPULimit, "cpu"), emphasis: "name"},
				{label: "mem", value: formatReqLim(c.MemRequest, c.MemLimit, "mem"), emphasis: "name"},
			},
		})
	}

	probes := []inspectorRow{}
	if c.Liveness != "" {
		probes = append(probes, inspectorRow{label: "liveness", value: c.Liveness, emphasis: "dim"})
	}
	if c.Readiness != "" {
		probes = append(probes, inspectorRow{label: "readiness", value: c.Readiness, emphasis: "dim"})
	}
	if c.Startup != "" {
		probes = append(probes, inspectorRow{label: "startup", value: c.Startup, emphasis: "dim"})
	}
	if len(probes) > 0 {
		s.sections = append(s.sections, inspectorSection{title: "probes", rows: probes})
	}

	wiring := []inspectorRow{}
	if c.Mounts > 0 {
		wiring = append(wiring, inspectorRow{
			label: "mounts", value: strconv.Itoa(c.Mounts), emphasis: "dim",
		})
	}
	if c.EnvCount > 0 || c.EnvFromCount > 0 {
		val := strconv.Itoa(c.EnvCount)
		if c.EnvFromCount > 0 {
			val += " · " + strconv.Itoa(c.EnvFromCount) + " envFrom"
		}
		wiring = append(wiring, inspectorRow{label: "env", value: val, emphasis: "dim"})
	}
	if len(wiring) > 0 {
		s.sections = append(s.sections, inspectorSection{rows: wiring})
	}
	return s
}

func inspectConfigMapSnap(c k8s.ConfigMapInfo) *inspectorSnapshot {
	// Row carries (ns, keys, age). Inspector previews the schema —
	// up to a handful of key names — so the operator sees what the
	// configmap is *about* without opening the reveal modal.
	s := &inspectorSnapshot{
		title:    "ConfigMap · " + c.Name,
		subtitle: "press d to view keys",
	}
	if rows := keyPreviewRows(c.KeyNames); len(rows) > 0 {
		s.sections = append(s.sections, inspectorSection{rows: rows})
	}
	return s
}

func inspectSecretSnap(sec k8s.SecretInfo) *inspectorSnapshot {
	s := &inspectorSnapshot{
		title:    "Secret · " + sec.Name,
		subtitle: "type · " + nonEmpty(sec.Type, "Opaque") + "  ·  press v to reveal",
	}
	if rows := keyPreviewRows(sec.KeyNames); len(rows) > 0 {
		s.sections = append(s.sections, inspectorSection{rows: rows})
	}
	return s
}

// keyPreviewRows folds a sorted key-name list into a single inspector row
// "keys · a, b, c (+N more)" — capped so a configmap with 50 entries
// doesn't take over the tile. Names only, never values.
func keyPreviewRows(names []string) []inspectorRow {
	if len(names) == 0 {
		return nil
	}
	const limit = 5
	preview := names
	suffix := ""
	if len(names) > limit {
		preview = names[:limit]
		suffix = "  (+" + strconv.Itoa(len(names)-limit) + " more)"
	}
	return []inspectorRow{{
		label:    "keys",
		value:    strings.Join(preview, ", ") + suffix,
		emphasis: "dim",
	}}
}

func inspectPVCSnap(p k8s.PVCInfo, det *inspectorDetail) *inspectorSnapshot {
	// Status / capacity / class are in the row's columns; the inspector
	// skips them. It surfaces the fields a row can't fit: the bound PV
	// name (when the row hides it), access modes, non-default volume
	// mode, the latest pending-condition message, and the pods that
	// currently have the claim mounted.
	s := &inspectorSnapshot{title: "PVC · " + p.Name}
	// Status headline drives the colour — same severity vocab the
	// row's STATUS column uses, but here it's the loud bit at the top
	// of the tile rather than a column entry.
	if p.Status != "" {
		s.statusText = p.Status
		s.statusKind = pvClaimStatusEmphasis(p.Status)
	}

	rows := []inspectorRow{}
	if det != nil && det.pvc != nil {
		d := det.pvc
		if d.AccessModes != "" {
			rows = append(rows, inspectorRow{label: "accessModes", value: d.AccessModes, emphasis: "name"})
		}
		// Block mode is the unusual setting that changes how the volume
		// is mounted; suppress the default Filesystem to keep the row
		// list quiet for the majority case.
		if d.VolumeMode != "" {
			rows = append(rows, inspectorRow{label: "volumeMode", value: d.VolumeMode, emphasis: "warn"})
		}
		if d.PendingReason != "" {
			rows = append(rows, inspectorRow{
				label: "pending", value: d.PendingReason, emphasis: "warn",
			})
		}
	}
	if len(rows) > 0 {
		s.sections = append(s.sections, inspectorSection{rows: rows})
	}
	if det != nil && det.pvc != nil && len(det.pvc.MountedBy) > 0 {
		s.sections = append(s.sections, inspectorSection{
			title: "mounted by",
			rows:  podMountRows(det.pvc.MountedBy),
		})
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

func inspectPVSnap(p k8s.PVInfo, det *inspectorDetail) *inspectorSnapshot {
	// Row carries status / capacity / claim / access / reclaim
	// (progressive disclosure for the last three). The inspector
	// surfaces the per-driver identity (CSI driver + handle), topology
	// (node affinity), and which workload currently mounts the
	// underlying disk — none of which fit in a row column.
	s := &inspectorSnapshot{title: "PV · " + p.Name}
	if p.Status != "" {
		s.statusText = p.Status
		s.statusKind = pvVolumeStatusEmphasis(p.Status)
	}
	// Reclaim policy is a strong-emphasis hint (Delete = red) that
	// belongs in the inspector even though the row carries it —
	// the column is dim-by-default, the inspector promotes the
	// dangerous case so an operator scanning top-down sees it.
	rows := []inspectorRow{}
	if p.Reclaim != "" {
		rows = append(rows, inspectorRow{label: "reclaim", value: p.Reclaim, emphasis: pvReclaimEmphasis(p.Reclaim)})
	}
	if det != nil && det.pv != nil {
		d := det.pv
		if d.CSIDriver != "" {
			rows = append(rows, inspectorRow{label: "csi driver", value: d.CSIDriver, emphasis: "name"})
		}
		if d.VolumeHandle != "" {
			rows = append(rows, inspectorRow{label: "handle", value: d.VolumeHandle, emphasis: "dim"})
		}
		if d.NodeAffinity != "" {
			rows = append(rows, inspectorRow{label: "topology", value: d.NodeAffinity, emphasis: "dim"})
		}
	}
	if len(rows) > 0 {
		s.sections = append(s.sections, inspectorSection{rows: rows})
	}
	if det != nil && det.pv != nil && len(det.pv.MountedBy) > 0 {
		s.sections = append(s.sections, inspectorSection{
			title: "mounted by",
			rows:  podMountRows(det.pv.MountedBy),
		})
	}
	if det != nil && det.err != nil {
		s.err = det.err
	}
	return s
}

// podMountRows folds a list of pod names (or "ns/name") into "pod" rows,
// capped so a heavily-shared claim doesn't fill the tile.
func podMountRows(pods []string) []inspectorRow {
	const limit = 5
	out := make([]inspectorRow, 0, len(pods))
	for i, p := range pods {
		if i == limit && len(pods) > limit {
			out = append(out, inspectorRow{
				label: "more", value: "+" + strconv.Itoa(len(pods)-limit), emphasis: "dim",
			})
			break
		}
		out = append(out, inspectorRow{label: "pod", value: p, emphasis: "name"})
	}
	return out
}

// pvClaimStatusEmphasis maps PVC status to an inspectorRow emphasis. Bound
// is the steady-state happy case (good); Lost or Failed flags trouble; Pending
// is a known-transient state worth a warn.
func pvClaimStatusEmphasis(status string) string {
	switch status {
	case "Bound":
		return "good"
	case "Pending":
		return "warn"
	case "Lost", "Failed":
		return "bad"
	}
	return ""
}

// pvVolumeStatusEmphasis is the PV-side mirror of pvClaimStatusEmphasis.
// Released sits between "happy" and "dangerous" — the volume held data and
// has been unbound, so a Delete reclaim will erase it on next reconcile.
func pvVolumeStatusEmphasis(status string) string {
	switch status {
	case "Bound", "Available":
		return "good"
	case "Released":
		return "warn"
	case "Failed":
		return "bad"
	}
	return ""
}

// pvReclaimEmphasis colours Delete red — the case where the on-caller most
// often wants to know "if I drop the claim, do I lose the data?". Retain is
// the safe default.
func pvReclaimEmphasis(reclaim string) string {
	switch reclaim {
	case "Delete":
		return "bad"
	case "Retain":
		return "good"
	}
	return ""
}

func inspectNodePoolSnap(p k8s.NodePoolInfo) *inspectorSnapshot {
	// Pool row carries Nodes/Pods/CPU/MEM; leave the inspector to
	// surface provider (not in the row) plus a tiny header.
	s := &inspectorSnapshot{title: "Node pool · " + p.Name}
	if p.Provider != "" {
		s.subtitle = "provider · " + p.Provider
	}
	return s
}

// ── headline / status helpers ───────────────────────────────────────

func podHeadline(p k8s.PodInfo) (string, string) {
	h := k8s.PodHealth(p)
	kind := healthKind(h)
	// Compose the headline from status + restart hint when restarts > 0,
	// so the operator sees both "Running but unhappy" cases at a glance.
	parts := []string{p.Status}
	if p.Restarts > 0 {
		parts = append(parts, fmt.Sprintf("%d restart", p.Restarts))
		if p.Restarts != 1 {
			parts[len(parts)-1] += "s"
		}
	}
	return strings.Join(parts, " · "), kind
}

func appHeadline(a k8s.AppInfo, det *inspectorDetail) (string, string) {
	kind := healthKind(a.Health)
	headline := healthWord(a.Health)
	// When detail is available, prefer the conditioned story ("Progressing"
	// vs "Failed" vs "Available"). We only override healthWord if a
	// condition is louder than the worst-of-pods rollup the tree
	// already computed.
	if det != nil && det.app != nil {
		if loudest := loudestCondition(det.app.Conditions); loudest != "" {
			headline = loudest
		}
	}
	return headline, kind
}

func nodeHeadline(n k8s.NodeInfo) (string, string) {
	kind := healthKind(n.Health)
	headline := n.Status
	if !n.Schedulable {
		headline += " · cordoned"
		if kind == "good" {
			kind = "warn"
		}
	}
	return headline, kind
}

// loudestCondition returns the most actionable condition string from a
// Deployment-style condition list — Failed/ReplicaFailure beats
// Progressing beats Available. Empty when nothing's interesting.
func loudestCondition(conds []k8s.AppCondition) string {
	var avail, prog, repl string
	for _, c := range conds {
		switch c.Type {
		case "Available":
			if c.Status == "True" {
				avail = "Available"
			} else {
				avail = "Unavailable"
			}
		case "Progressing":
			if c.Status == "True" {
				prog = "Progressing"
				if c.Reason == "NewReplicaSetAvailable" {
					prog = "" // suppress the steady-state "True" we don't need to shout
				}
			} else {
				prog = "Rollout stalled"
			}
		case "ReplicaFailure":
			if c.Status == "True" {
				repl = "ReplicaFailure"
			}
		}
	}
	if repl != "" {
		return repl
	}
	if prog != "" {
		return prog
	}
	return avail
}

// abnormalConditions filters out Available=True and the steady-state
// Progressing=True so the conditions section only lights up when
// something's actually wrong.
func abnormalConditions(conds []k8s.AppCondition) []inspectorRow {
	out := []inspectorRow{}
	for _, c := range conds {
		// Skip the boring happy paths.
		if c.Type == "Available" && c.Status == "True" {
			continue
		}
		if c.Type == "Progressing" && c.Status == "True" && c.Reason == "NewReplicaSetAvailable" {
			continue
		}
		em := "warn"
		if c.Status == "True" && c.Type == "ReplicaFailure" {
			em = "bad"
		}
		val := c.Reason
		if val == "" {
			val = c.Status
		}
		out = append(out, inspectorRow{label: c.Type, value: val, emphasis: em})
	}
	return out
}

// rolloutRows builds the "rollout" section for Deployment-style apps —
// updated/ready/desired plus strategy and selector when set.
func rolloutRows(d *k8s.AppDetail) []inspectorRow {
	rows := []inspectorRow{}
	switch d.Kind {
	case k8s.AppKindCronJob:
		if d.Schedule != "" {
			rows = append(rows, inspectorRow{label: "schedule", value: d.Schedule, emphasis: "name"})
		}
		if d.LastSchedule != "" {
			rows = append(rows, inspectorRow{label: "last run", value: d.LastSchedule, emphasis: "dim"})
		}
		if d.Suspended {
			rows = append(rows, inspectorRow{label: "state", value: "suspended", emphasis: "warn"})
		}
	case k8s.AppKindJob:
		rows = append(rows, inspectorRow{
			label: "active", value: strconv.Itoa(int(d.JobActive)),
		})
		rows = append(rows, inspectorRow{
			label: "succeeded", value: strconv.Itoa(int(d.JobSucceeded)), emphasis: "good",
		})
		if d.JobFailed > 0 {
			rows = append(rows, inspectorRow{
				label: "failed", value: strconv.Itoa(int(d.JobFailed)), emphasis: "bad",
			})
		}
	default:
		rows = append(rows, inspectorRow{
			label: "replicas",
			value: fmt.Sprintf("%d updated · %d ready · %d desired",
				d.UpdatedReplicas, d.ReadyReplicas, d.DesiredReplicas),
			emphasis: "name",
		})
		if d.Strategy != "" {
			rows = append(rows, inspectorRow{label: "strategy", value: d.Strategy, emphasis: "dim"})
		}
		if d.Generation > 0 && d.ObservedGen < d.Generation {
			rows = append(rows, inspectorRow{
				label:    "generation",
				value:    fmt.Sprintf("%d desired / %d observed (rollout pending)", d.Generation, d.ObservedGen),
				emphasis: "warn",
			})
		}
		if d.Selector != "" {
			rows = append(rows, inspectorRow{label: "selector", value: d.Selector, emphasis: "dim"})
		}
	}
	return rows
}

func containerRow(c k8s.ContainerSnap) inspectorRow {
	// Trim sha digest suffix and `image:tag` prefix down to the short
	// final segment + tag — full repo paths blow out the column on
	// images like gcr.io/some-project/dir/dir/image:tag@sha256:...
	short := shortenImage(c.Image)
	val := short
	if !c.Ready {
		val = "✗ " + val
	}
	em := "name"
	if !c.Ready {
		em = "bad"
	}
	if strings.HasPrefix(c.State, "Waiting") && c.State != "Waiting:" {
		em = "bad"
	}
	if c.Restarts > 0 {
		val += fmt.Sprintf("  (×%d)", c.Restarts)
	}
	return inspectorRow{label: c.Name, value: val, emphasis: em}
}

// shortenImage trims a fully-qualified image to "<last segment>:<tag>"
// dropping any @sha256:… digest. We keep the tag because the operator
// usually cares about "which version is this" more than registry path.
func shortenImage(s string) string {
	if at := strings.Index(s, "@sha256:"); at >= 0 {
		s = s[:at]
	}
	// Take the trailing path segment so "ghcr.io/co/checkout:v1.2.3"
	// renders as "checkout:v1.2.3".
	if slash := strings.LastIndex(s, "/"); slash >= 0 {
		s = s[slash+1:]
	}
	return s
}

func containerStateKind(state string, ready bool) string {
	if strings.HasPrefix(state, "Waiting:CrashLoopBackOff") ||
		strings.HasPrefix(state, "Waiting:ImagePullBackOff") ||
		strings.HasPrefix(state, "Waiting:ErrImagePull") ||
		strings.HasPrefix(state, "Terminated:OOMKilled") ||
		strings.HasPrefix(state, "Terminated:Error") {
		return "bad"
	}
	if strings.HasPrefix(state, "Waiting") {
		return "warn"
	}
	if ready {
		return "good"
	}
	return ""
}

// formatScheduledAllocatable renders a node's "X / Y" sister stat —
// pod-request sum vs. allocatable — without the "req · lim" wording
// formatReqLim uses, which would mislabel a node's scheduler reservation
// as a limit.
func formatScheduledAllocatable(scheduled, allocatable int64, kind string) string {
	format := k8s.FormatCPU
	if kind == "mem" {
		format = k8s.FormatMem
	}
	switch {
	case scheduled > 0 && allocatable > 0:
		return format(scheduled) + " / " + format(allocatable)
	case allocatable > 0:
		return "— / " + format(allocatable)
	case scheduled > 0:
		return format(scheduled) + " / —"
	}
	return "—"
}

func hasResources(cpuReq, cpuLim, memReq, memLim int64) bool {
	return cpuReq > 0 || cpuLim > 0 || memReq > 0 || memLim > 0
}

func formatReqLim(req, lim int64, kind string) string {
	format := k8s.FormatCPU
	if kind == "mem" {
		format = k8s.FormatMem
	}
	switch {
	case req > 0 && lim > 0 && req == lim:
		return format(req) + "  (req=lim)"
	case req > 0 && lim > 0:
		return fmt.Sprintf("%s req · %s lim", format(req), format(lim))
	case req > 0:
		return format(req) + " req · — lim"
	case lim > 0:
		return "— req · " + format(lim) + " lim"
	}
	return "—"
}

// ── health vocab helpers ────────────────────────────────────────────

// healthKind maps a k8s.Health to the inspector's "good/warn/bad" tag.
func healthKind(h k8s.Health) string {
	switch h {
	case k8s.HealthHealthy:
		return "good"
	case k8s.HealthInProgress, k8s.HealthDegraded:
		return "warn"
	case k8s.HealthFailing:
		return "bad"
	case k8s.HealthUnknown:
		return ""
	default:
		return ""
	}
}

func healthWord(h k8s.Health) string {
	switch h {
	case k8s.HealthHealthy:
		return "Healthy"
	case k8s.HealthInProgress:
		return "Progressing"
	case k8s.HealthDegraded:
		return "Degraded"
	case k8s.HealthFailing:
		return "Failing"
	case k8s.HealthUnknown:
		return ""
	default:
		return ""
	}
}

// ── tiny helpers ────────────────────────────────────────────────────

func stringsTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW < 2 {
		return strings.Repeat(".", maxW)
	}
	rs := []rune(s)
	if len(rs) <= maxW-1 {
		return s
	}
	return string(rs[:maxW-1]) + "…"
}

// ── tree selection helpers (read t.cursor for kinds the Tree doesn't expose) ──

func selectedContainer(t *Tree) (k8s.ContainerInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindContainer {
		return k8s.ContainerInfo{}, false
	}
	if t.cursor < 0 || t.cursor >= len(ch.Containers) {
		return k8s.ContainerInfo{}, false
	}
	return ch.Containers[t.cursor], true
}

func selectedService(t *Tree) (k8s.ServiceInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindService {
		return k8s.ServiceInfo{}, false
	}
	rows := t.filteredServices()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.ServiceInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedIngress(t *Tree) (k8s.IngressInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindIngress {
		return k8s.IngressInfo{}, false
	}
	rows := t.filteredIngresses()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.IngressInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedConfigMap(t *Tree) (k8s.ConfigMapInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindConfigMap {
		return k8s.ConfigMapInfo{}, false
	}
	rows := t.filteredConfigMaps()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.ConfigMapInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedSecret(t *Tree) (k8s.SecretInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindSecret {
		return k8s.SecretInfo{}, false
	}
	rows := t.filteredSecrets()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.SecretInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedPVC(t *Tree) (k8s.PVCInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindPVC {
		return k8s.PVCInfo{}, false
	}
	rows := t.filteredPVCs()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.PVCInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedPV(t *Tree) (k8s.PVInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindPV {
		return k8s.PVInfo{}, false
	}
	rows := t.filteredPVs()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return k8s.PVInfo{}, false
	}
	return rows[t.cursor], true
}

func selectedNodePool(t *Tree) (k8s.NodePoolInfo, bool) {
	ch := t.Children()
	if ch == nil || ch.Kind != k8s.KindNodePool {
		return k8s.NodePoolInfo{}, false
	}
	if t.cursor < 0 || t.cursor >= len(ch.Pools) {
		return k8s.NodePoolInfo{}, false
	}
	return ch.Pools[t.cursor], true
}
