package sections

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"k8s.io/apimachinery/pkg/types"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// NodeRef is the typed identifier for a level in the breadcrumb stack.
type NodeRef struct {
	Kind      string // k8s.KindRoot, KindApp, KindPod, KindContainer, KindNodePool, KindNode, KindSystemFold, KindNamespace
	SubKind   string // for KindApp: AppKindDeployment / StatefulSet / DaemonSet / CronJob / Job
	Namespace string
	Name      string
	UID       types.UID
	// Display is the label used when rendering the breadcrumb segment for
	// this ref (so we can render even when the live data has rotated out).
	Display string
	Health  k8s.Health
}

// Children is the polymorphic payload for a single tree level. Exactly one
// typed slice is populated, matching `Kind`.
type Children struct {
	Kind        string
	Contexts    []config.ContextInfo // populated for KindContextList
	Apps        []k8s.AppInfo
	Pods        []k8s.PodInfo
	Containers  []k8s.ContainerInfo
	Pools       []k8s.NodePoolInfo
	Nodes       []k8s.NodeInfo
	Namespaces  []k8s.NamespaceInfo
	SystemCount int      // when > 0 in an Apps root, append a synthetic SystemFold row
	SystemNS    []string // populated alongside SystemCount
}

// ── Tree section ─────────────────────────────────────────────────────

// Tree is the breadcrumb-driven drill navigator that replaces the previous
// Deployments + Pods two-panel layout.
type Tree struct {
	Focused bool
	Loading bool

	// LoadErr is the last error from the loader for the current focus.
	// Distinguishes "API call failed" from "API returned zero items" so the
	// renderer can show a retry hint in the failure case instead of the
	// indistinguishable empty placeholder. Cleared on the next successful
	// SetChildren or on a focus change (Push / Pop / ResetForContextSwitch).
	LoadErr error

	path []NodeRef

	// children is the loaded data for the current focus (path[-1]).
	children *Children

	// cursorByKey preserves selection across drill+pop.
	cursorByKey map[string]int

	// cache holds previously-loaded levels for instant Esc-back.
	cache map[string]*Children

	filter    components.Filter
	alphaSort bool

	cursor int
}

// NewTree builds an empty Tree rooted at the unified root.
func NewTree(c *tuictx.Ctx) Tree {
	t := Tree{
		cursorByKey: make(map[string]int),
		cache:       make(map[string]*Children),
		filter:      components.NewFilter(c.Theme),
	}
	t.path = []NodeRef{rootRef()}
	return t
}

func rootRef() NodeRef {
	return NodeRef{
		Kind:    k8s.KindContextList,
		Name:    "Clusters",
		Display: "Clusters",
	}
}

// SeedContexts pre-fills the cache for the root level with the static list
// of kube contexts. The context list isn't a k8s API call — we have it in
// hand from kubeconfig — so there's no need to round-trip through a loader.
func (t *Tree) SeedContexts(items []config.ContextInfo) {
	rootKey := pathKeyFor([]NodeRef{rootRef()})
	t.cache[rootKey] = &Children{Kind: k8s.KindContextList, Contexts: items}
	if t.pathKey() == rootKey {
		t.children = t.cache[rootKey]
	}
}

// PushContext drills the tree one level into the named context. Used by the
// root when the user picks a cluster from the picker.
func (t *Tree) PushContext(name string) tea.Cmd {
	return t.Push(NodeRef{
		Kind:    k8s.KindContext,
		Name:    name,
		Display: name,
	})
}

// pathKey serializes the path stack to a stable string for cache + cursor.
func (t *Tree) pathKey() string { return pathKeyFor(t.path) }

func pathKeyFor(p []NodeRef) string {
	parts := make([]string, len(p))
	for i, r := range p {
		parts[i] = r.Kind + "/" + r.Namespace + "/" + r.Name + "/" + string(r.UID)
	}
	return strings.Join(parts, "|")
}

// Path returns the current breadcrumb path (read-only).
func (t *Tree) Path() []NodeRef { return t.path }

// FilterActive reports whether the tree's filter input is currently
// receiving keystrokes. The root checks this before consuming letter keys
// (e.g. `i` for Ask) so the user can type literal letters into the filter
// without accidentally jumping into the chat panel.
func (t *Tree) FilterActive() bool { return t.filter.Active() }

// YankPayload returns the kubectl-pasteable identifier for the currently
// focused row — empty if there is nothing selectable. We bias toward
// "<namespace>/<name>" for namespaced resources because that's unambiguous
// when copied into a fresh terminal where the operator hasn't already
// scoped to a namespace. Cluster-level rows return their bare name.
func (t *Tree) YankPayload() string {
	ref := t.SelectedRef()
	switch ref.Kind {
	case k8s.KindContext, k8s.KindNode, k8s.KindNodePool, k8s.KindNamespace:
		return ref.Name
	case k8s.KindApp, k8s.KindPod:
		if ref.Namespace == "" {
			return ref.Name
		}
		return ref.Namespace + "/" + ref.Name
	case k8s.KindContainer:
		// The container's Namespace is the parent pod's; Name is the
		// container itself. Surface both so a paste like `kubectl exec -it
		// <ns>/<pod> -c <container>` can be assembled at a glance — we
		// don't have the pod's name on the ref alone, so fall back to the
		// breadcrumb path if it's available.
		if len(t.path) >= 2 {
			pod := t.path[len(t.path)-1]
			if pod.Kind == k8s.KindPod && pod.Namespace != "" {
				return pod.Namespace + "/" + pod.Name + "/" + ref.Name
			}
		}
		return ref.Name
	}
	return ""
}

// Focus is the current parent ref (path[-1]).
func (t *Tree) Focus() NodeRef {
	if len(t.path) == 0 {
		return rootRef()
	}
	return t.path[len(t.path)-1]
}

// Children returns the loaded children for the current focus, or nil while
// loading or unloaded. Callers should also check Tree.Loading.
func (t *Tree) Children() *Children { return t.children }

// SelectedRef returns the NodeRef of the highlighted child, or zero value
// when the level is empty.
func (t *Tree) SelectedRef() NodeRef {
	rows := t.visibleRows()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return NodeRef{}
	}
	return rows[t.cursor]
}

// SelectedAppInfo returns the highlighted AppInfo and true when the focus
// renders apps; otherwise zero value and false. (Convenience for the action
// dispatcher in the root model.)
func (t *Tree) SelectedAppInfo() (k8s.AppInfo, bool) {
	if t.children == nil || t.children.Kind != k8s.KindApp {
		return k8s.AppInfo{}, false
	}
	apps := t.filteredApps()
	if t.cursor < 0 || t.cursor >= len(apps) {
		return k8s.AppInfo{}, false
	}
	return apps[t.cursor], true
}

// SelectedPodInfo returns the highlighted PodInfo when the focus renders
// pods; otherwise zero value and false.
func (t *Tree) SelectedPodInfo() (k8s.PodInfo, bool) {
	if t.children == nil || t.children.Kind != k8s.KindPod {
		return k8s.PodInfo{}, false
	}
	pods := t.filteredPods()
	if t.cursor < 0 || t.cursor >= len(pods) {
		return k8s.PodInfo{}, false
	}
	return pods[t.cursor], true
}

// SetChildren stores the loaded payload for the given path and updates the
// current view if that path is still the focus.
//
// Cursor preservation:
//   - On a refresh (auto-tick or manual `R`), keep the user on the same row
//     by name: capture the highlighted ref before swapping children, then
//     relocate it in the new list.
//   - On first arrival after a Push (no prior children), use the cursor
//     value Push seeded from cursorByKey.
//   - When the named row is gone, clamp the existing cursor into range
//     instead of jumping to 0 — that way "next neighbour" is still in view
//     after a delete/scale-down.
func (t *Tree) SetChildren(path []NodeRef, ch *Children) {
	key := pathKeyFor(path)
	t.cache[key] = ch
	if key != t.pathKey() {
		return
	}

	t.LoadErr = nil

	prev := t.SelectedRef()
	t.children = ch

	if prev.Kind != "" {
		for i, r := range t.visibleRows() {
			if r.Kind == prev.Kind && r.Name == prev.Name && r.Namespace == prev.Namespace {
				t.cursor = i
				return
			}
		}
	}

	n := t.visibleCount()
	if n == 0 {
		t.cursor = 0
		return
	}
	if t.cursor >= n {
		t.cursor = n - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// Push drills into the given child. Returns a TreeFocusChangedMsg cmd so the
// root model can issue a load. The new level is empty until SetChildren
// arrives.
func (t *Tree) Push(ref NodeRef) tea.Cmd {
	t.cursorByKey[t.pathKey()] = t.cursor
	t.path = append(t.path, ref)
	t.children = t.cache[t.pathKey()]
	t.cursor = t.cursorByKey[t.pathKey()]
	t.LoadErr = nil
	t.filter.Clear()
	return emit(TreeFocusChangedMsg{Path: clonePath(t.path)})
}

// Pop returns to the parent level. Cursor for the popped level is preserved.
func (t *Tree) Pop() tea.Cmd {
	if len(t.path) <= 1 {
		return nil // already at root
	}
	t.cursorByKey[t.pathKey()] = t.cursor
	t.path = t.path[:len(t.path)-1]
	t.children = t.cache[t.pathKey()]
	t.cursor = t.cursorByKey[t.pathKey()]
	t.LoadErr = nil
	t.filter.Clear()
	return emit(TreeFocusChangedMsg{Path: clonePath(t.path)})
}

// InvalidateFocus drops the cache entry for the current focus so the next
// load fetches fresh data. Called after a successful action (scale, etc.).
//
// The context-list root is *seeded* from kubeconfig (not loaded from k8s)
// and is immutable for the lifetime of the program — invalidating it would
// just blank the picker, since the no-op loader for KindContextList returns
// empty children. Skip it so a stray ActionDoneMsg landing while the user
// is back at the picker can't wipe the seeded list.
func (t *Tree) InvalidateFocus() {
	if t.Focus().Kind == k8s.KindContextList {
		return
	}
	delete(t.cache, t.pathKey())
	if len(t.path) >= 2 {
		parent := pathKeyFor(t.path[:len(t.path)-1])
		delete(t.cache, parent)
	}
}

// ResetForContextSwitch drops every cached level *below* the context-list
// root, so the next drill into any context fetches fresh data. The root
// (KindContextList) cache survives — its content is the kubeconfig contexts
// list, which doesn't change at runtime.
func (t *Tree) ResetForContextSwitch() {
	rootKey := pathKeyFor([]NodeRef{rootRef()})
	rootCache := t.cache[rootKey]
	t.cache = make(map[string]*Children)
	if rootCache != nil {
		t.cache[rootKey] = rootCache
	}
	t.cursorByKey = make(map[string]int)
	t.filter.Clear()
}

// ── Update ───────────────────────────────────────────────────────────

func (t *Tree) Update(msg tea.Msg, c *tuictx.Ctx) tea.Cmd {
	if !t.Focused {
		return nil
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if t.filter.Active() {
		return t.updateFiltering(km)
	}

	switch {
	case key.Matches(km, c.Keys.Filter):
		t.filter.Open()
		return nil
	case key.Matches(km, c.Keys.Up):
		t.cursor = clamp(t.cursor-1, 0, t.visibleCount()-1)
	case key.Matches(km, c.Keys.Down):
		t.cursor = clamp(t.cursor+1, 0, t.visibleCount()-1)
	case km.String() == "g":
		t.cursor = 0
	case km.String() == "G":
		if n := t.visibleCount(); n > 0 {
			t.cursor = n - 1
		}
	case km.String() == "o":
		t.alphaSort = !t.alphaSort
	case key.Matches(km, c.Keys.FirstFail):
		t.cursor = t.firstUnhealthyIndex()
	case key.Matches(km, c.Keys.Enter):
		ref := t.SelectedRef()
		if ref.Kind == "" {
			return nil
		}
		// Container is terminal — no further drill. Logs are bound to `l`.
		if ref.Kind == k8s.KindContainer {
			return nil
		}
		return t.Push(ref)
	case key.Matches(km, c.Keys.Back):
		// If a filter query is sticky (input closed but value present), clear
		// before popping.
		if t.filter.Value() != "" {
			t.filter.Clear()
			t.cursor = 0
			return nil
		}
		if len(t.path) > 1 {
			return t.Pop()
		}
	}
	return nil
}

func (t *Tree) updateFiltering(km tea.KeyPressMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		t.filter.Cancel()
		t.cursor = 0
		return nil
	case "enter":
		t.filter.Close()
		return nil
	}
	cmd := t.filter.Update(km)
	t.cursor = 0
	return cmd
}

// ── Render ───────────────────────────────────────────────────────────

func (t *Tree) View(c *tuictx.Ctx, w, h int) string {
	th := c.Theme

	// Title is a stable "Resource" label. The breadcrumb path lives in the
	// topbar — that's where users instinctively look for "where am I", so
	// putting it at the top of the screen frees this panel header from
	// repeating itself on every drill.
	innerW := components.PanelContentWidth(w)
	title := t.PanelTitle()

	// Title goes Dim, not cyan-bold — it's a label, not a headline. The
	// loudest text on this panel should be the data rows, and the loudest
	// chrome accent should be the cyan border (focus marker). A cyan title
	// stacked under the cyan logo in the topbar made both compete; pulling
	// the title down to Dim reorders the hierarchy so the eye flows
	// logo → breadcrumb → rows without the title interrupting.
	render := func(content string) string {
		if t.filter.Active() {
			return components.PanelWithAccent(th, title, w, h, content, t.Focused, th.P.Pink, th.P.Dim)
		}
		return components.PanelWithAccent(th, title, w, h, content, t.Focused, th.P.Cyan, th.P.Dim)
	}

	// Reserved rows: col header (1) + optional filter (1).
	reserved := 1
	if t.filter.Active() {
		reserved++
	}
	rowsAvail := h - 2 - reserved
	if rowsAvail < 1 {
		rowsAvail = 1
	}

	body := t.renderBody(c, innerW, rowsAvail)

	parts := []string{}
	if t.filter.Active() {
		// Pin the filter input to the panel's content width so its trailing
		// boundary doesn't shift between renders. Without this, the textinput
		// emits a string sized to its content, and the diff renderer may leave
		// stale cells (border glyphs) past the input's right edge after the
		// filter closes.
		t.filter.SetWidth(innerW)
		parts = append(parts, t.filter.View())
	}
	parts = append(parts, body)
	return render(strings.Join(parts, "\n"))
}

// BreadcrumbCrumbs returns the current path as a slice of components.Crumb
// for the topbar's location strip. We deliberately omit kind glyphs here:
// breadcrumb segments are already named ("prod-east-1" reads as a cluster,
// "my-app" as an app) so a leading glyph adds visual noise without adding
// information. Glyphs earn their place in *row* lists where many similar
// names compete and a 1-cell kind cue speeds scanning — the topbar is a
// short path of distinct, self-labelling names.
func (t *Tree) BreadcrumbCrumbs() []components.Crumb {
	out := make([]components.Crumb, len(t.path))
	for i, r := range t.path {
		out[i] = components.Crumb{Label: r.Display}
	}
	return out
}

// PanelTitle returns the kind label for the current view — "Clusters",
// "Apps", "Pods", "Containers", and so on. This is the answer to "what
// kind am I looking at"; the topbar breadcrumb answers "how did I get
// here". The row count is left to the data itself — counting is the
// reader's job, not the chrome's.
func (t *Tree) PanelTitle() string {
	if t.children != nil {
		switch t.children.Kind {
		case k8s.KindContextList:
			return "Clusters"
		case k8s.KindApp:
			return "Apps"
		case k8s.KindPod:
			return "Pods"
		case k8s.KindContainer:
			return "Containers"
		case k8s.KindNodePool:
			return "Node Pools"
		case k8s.KindNode:
			return "Nodes"
		case k8s.KindNamespace:
			return "Namespaces"
		}
	}
	return childKindLabel(t.Focus().Kind)
}

// childKindLabel maps a focus kind to the plural noun for what its children
// will be — used as the panel title while a level is still loading.
func childKindLabel(focusKind string) string {
	switch focusKind {
	case k8s.KindContextList:
		return "Clusters"
	case k8s.KindContext, k8s.KindNamespace:
		return "Apps"
	case k8s.KindApp:
		return "Pods"
	case k8s.KindPod:
		return "Containers"
	case k8s.KindClusterFold:
		return "Node Pools"
	case k8s.KindNodePool, k8s.KindNode:
		return "Nodes"
	case k8s.KindSystemFold:
		return "Namespaces"
	}
	return ""
}

func pctOf(used, total int64) string {
	if total <= 0 {
		return "—"
	}
	pct := used * 100 / total
	return fmt.Sprintf("%d%%", pct)
}

// ── Body rendering (per-kind dispatch) ───────────────────────────────

func (t *Tree) renderBody(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme

	if t.Loading && (t.children == nil) {
		return th.Idle.Render(c.Spinner.View() + " loading")
	}
	// Distinguish "load failed" from "no children yet" (in-flight) and from
	// "API said zero rows" (handled by the per-kind renderers below). Without
	// this branch, an unreachable cluster falls through to a silent ellipsis
	// while the operator wonders whether the tree is just slow or broken.
	if t.children == nil && t.LoadErr != nil {
		hint := th.DimTxt.Italic(true).Render("press R to retry")
		return lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(th.P.Red).Render("✗ "+t.LoadErr.Error()),
			"",
			hint,
		)
	}
	if t.children == nil {
		return th.Idle.Render("…")
	}

	switch t.children.Kind {
	case k8s.KindContextList:
		return t.renderContextRows(c, innerW, rowsAvail)
	case k8s.KindApp:
		return t.renderAppRows(c, innerW, rowsAvail)
	case k8s.KindPod:
		return t.renderPodRows(c, innerW, rowsAvail)
	case k8s.KindContainer:
		return t.renderContainerRows(c, innerW, rowsAvail)
	case k8s.KindNodePool:
		return t.renderPoolRows(c, innerW, rowsAvail)
	case k8s.KindNode:
		return t.renderNodeRows(c, innerW, rowsAvail)
	case k8s.KindNamespace:
		return t.renderNamespaceRows(c, innerW, rowsAvail)
	}
	return th.Idle.Render("(empty)")
}

// visibleRows returns the list of refs corresponding to the current rendered
// rows, in display order — used by SelectedRef and Enter dispatch.
func (t *Tree) visibleRows() []NodeRef {
	if t.children == nil {
		return nil
	}
	switch t.children.Kind {
	case k8s.KindContextList:
		ctxs := t.filteredContexts()
		out := make([]NodeRef, len(ctxs))
		for i, ci := range ctxs {
			out[i] = NodeRef{
				Kind:    k8s.KindContext,
				Name:    ci.Name,
				Display: ci.Name,
			}
		}
		return out
	case k8s.KindApp:
		apps := t.filteredApps()
		atRoot := t.Focus().Kind == k8s.KindContext
		out := make([]NodeRef, 0, len(apps)+2)
		// Cluster lives at the very top of the root so the on-caller's eye
		// lands on infra capacity first; System tucks at the bottom so it
		// stays out of the scan path. Both are hidden once a filter is on.
		if atRoot && t.filter.Value() == "" {
			out = append(out, NodeRef{
				Kind:    k8s.KindClusterFold,
				Name:    "Cluster",
				Display: "Cluster",
				Health:  k8s.HealthUnknown,
			})
		}
		for _, a := range apps {
			out = append(out, NodeRef{
				Kind: k8s.KindApp, SubKind: a.Kind,
				Namespace: a.Namespace, Name: a.Name, UID: a.UID,
				Display: a.Name, Health: a.Health,
			})
		}
		if atRoot && t.children.SystemCount > 0 && t.filter.Value() == "" {
			out = append(out, NodeRef{
				Kind:    k8s.KindSystemFold,
				Name:    "System",
				Display: fmt.Sprintf("System (%d)", t.children.SystemCount),
				Health:  k8s.HealthUnknown,
			})
		}
		return out
	case k8s.KindPod:
		pods := t.filteredPods()
		out := make([]NodeRef, len(pods))
		for i, p := range pods {
			out[i] = NodeRef{
				Kind: k8s.KindPod, Namespace: p.Namespace, Name: p.Name,
				Display: p.Name, Health: k8s.PodHealth(p),
			}
		}
		return out
	case k8s.KindContainer:
		ctrs := t.filteredContainers()
		out := make([]NodeRef, len(ctrs))
		for i, ci := range ctrs {
			out[i] = NodeRef{
				Kind: k8s.KindContainer, Namespace: ci.Namespace, Name: ci.Name,
				Display: ci.Name, Health: k8s.ContainerHealth(ci),
			}
		}
		return out
	case k8s.KindNodePool:
		pools := t.filteredPools()
		out := make([]NodeRef, len(pools))
		for i, p := range pools {
			out[i] = NodeRef{Kind: k8s.KindNodePool, Name: p.Name, Display: p.Name, Health: p.Health}
		}
		return out
	case k8s.KindNode:
		nodes := t.filteredNodes()
		out := make([]NodeRef, len(nodes))
		for i, n := range nodes {
			out[i] = NodeRef{Kind: k8s.KindNode, Name: n.Name, UID: n.UID, Display: n.Name, Health: n.Health}
		}
		return out
	case k8s.KindNamespace:
		nss := t.filteredNamespaces()
		out := make([]NodeRef, len(nss))
		for i, n := range nss {
			out[i] = NodeRef{Kind: k8s.KindNamespace, Name: n.Name, Display: n.Name}
		}
		return out
	}
	return nil
}

func (t *Tree) visibleCount() int { return len(t.visibleRows()) }

// ── Per-kind row rendering ───────────────────────────────────────────

func (t *Tree) renderAppRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	apps := t.filteredApps()
	rows := t.visibleRows()
	if len(rows) == 0 {
		return th.Idle.Render("no apps")
	}

	// Progressive disclosure of columns: NS only when there's more than one
	// distinct user namespace on screen (no point showing "default" 50×);
	// CPU/MEM only when at least one row has a real value (drops the column
	// entirely on clusters where metrics-server is absent — replacing 20 rows
	// of "—" with extra width for NAME). Reclaimed cells flow into NAME so
	// long deployment names stop truncating.
	showNS := distinctAppNamespaces(apps) > 1
	showMetrics := anyAppMetrics(apps)

	tableW := innerW - components.EdgeTotal()
	const (
		kW      = 1
		hW      = 1
		cpuW    = 6
		memW    = 6
		agW     = 5
		nameMin = 10
	)
	nsW := 10
	hpaW := 11
	if !showNS {
		nsW = 0
	}
	colsCount := 5 // K, NAME, H, HPA, AGE — always
	if showNS {
		colsCount++
	}
	if showMetrics {
		colsCount += 2
	}
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		usedCpuW = cpuW
		usedMemW = memW
	}
	for {
		gaps := components.GapTotal(colsCount)
		nW := tableW - kW - nsW - hW - hpaW - usedCpuW - usedMemW - agW - gaps
		if nW >= nameMin {
			break
		}
		if showNS && nsW > 4 {
			nsW--
			continue
		}
		if hpaW > 4 {
			hpaW--
			continue
		}
		break
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - nsW - hW - hpaW - usedCpuW - usedMemW - agW - gaps
	if nW < nameMin {
		nW = nameMin
	}

	// Column order: K · NAME · NS? · H · HPA · CPU? · MEM? · AGE.
	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME"}
	if showNS {
		cols = append(cols, nsW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "NS")
	}
	cols = append(cols, hW, hpaW)
	aligns = append(aligns, components.AlignLeft, components.AlignLeft)
	headerLabels = append(headerLabels, "", "HPA")
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	cols = append(cols, agW)
	aligns = append(aligns, components.AlignRight)
	headerLabels = append(headerLabels, "AGE")
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(rows), rowsAvail-1)
	out := []string{header}
	// Compute how many rows precede the apps slice (a Cluster sentinel
	// occupies index 0 when present), so we can map row index → app index.
	appOffset := 0
	if len(rows) > 0 && rows[0].Kind == k8s.KindClusterFold {
		appOffset = 1
	}
	for i := start; i < end; i++ {
		ref := rows[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		if ref.Kind == k8s.KindClusterFold || ref.Kind == k8s.KindSystemFold {
			vals := []string{
				kindGlyph(ref.Kind, ""),
				th.NameStrongOn(components.TruncPad(ref.Display, nW), bg),
			}
			for j := 2; j < len(cols); j++ {
				vals = append(vals, "")
			}
			out = append(out, components.Row(th, vals, cols, aligns, selected, i))
			continue
		}
		var a k8s.AppInfo
		ai := i - appOffset
		if ai >= 0 && ai < len(apps) {
			a = apps[ai]
		}
		health := healthGlyphColored(th, a.Health, bg)
		var hpaCell string
		if a.HPA != nil {
			hpaText := components.TruncPad(fmt.Sprintf("↕%d (%d–%d)", a.HPA.Current, a.HPA.Min, a.HPA.Max), hpaW)
			if a.HPA.PinnedAtMax {
				hpaCell = bg.Foreground(th.P.Red).Bold(true).Render(hpaText)
			} else {
				hpaCell = th.MetaOn(hpaText, bg)
			}
		}
		nameCell := th.NameStrongOn(components.TruncPad(a.Name, nW), bg)
		ageCell := th.Age(a.Age, bg)

		vals := []string{appKindGlyph(a.Kind), nameCell}
		if showNS {
			vals = append(vals, th.MetaOn(components.TruncPad(a.Namespace, nsW), bg))
		}
		vals = append(vals, health, hpaCell)
		if showMetrics {
			vals = append(vals,
				th.MetaOn(k8s.FormatCPU(a.CPU), bg),
				th.MetaOn(k8s.FormatMem(a.Memory), bg),
			)
		}
		vals = append(vals, ageCell)

		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// distinctAppNamespaces counts the unique namespaces in the visible app list.
// Used to decide whether the NS column carries any signal.
func distinctAppNamespaces(apps []k8s.AppInfo) int {
	if len(apps) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(apps))
	for _, a := range apps {
		seen[a.Namespace] = struct{}{}
	}
	return len(seen)
}

// anyAppMetrics reports whether at least one app has live metrics. When
// false (metrics-server unavailable / not yet propagated) the CPU/MEM
// columns drop entirely.
func anyAppMetrics(apps []k8s.AppInfo) bool {
	for _, a := range apps {
		if a.CPU > 0 || a.Memory > 0 {
			return true
		}
	}
	return false
}

func (t *Tree) renderPodRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pods := t.filteredPods()
	if len(pods) == 0 {
		return th.Idle.Render("no pods")
	}
	showMetrics := anyPodMetrics(pods)

	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		stW  = 18
		cpuW = 6
		memW = 7
		rsW  = 4
		agW  = 5
	)
	colsCount := 5 // K, NAME, STATUS, RST, AGE
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		colsCount += 2
		usedCpuW, usedMemW = cpuW, memW
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - stW - usedCpuW - usedMemW - rsW - agW - gaps
	if nW < 10 {
		nW = 10
	}

	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME"}
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	cols = append(cols, stW, rsW, agW)
	aligns = append(aligns, components.AlignLeft, components.AlignRight, components.AlignRight)
	headerLabels = append(headerLabels, "STATUS", "RST", "AGE")
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pods), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pods[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nW), bg)
		statusCell := th.StatusGlyph(p.Status, stW, bg)
		rstCell := th.RestartCount(int(p.Restarts), bg)
		ageCell := th.Age(p.Age, bg)

		vals := []string{kindGlyph(k8s.KindPod, ""), nameCell}
		if showMetrics {
			vals = append(vals,
				th.MetaOn(k8s.FormatCPU(p.CPU), bg),
				th.MetaOn(k8s.FormatMem(p.Memory), bg),
			)
		}
		vals = append(vals, statusCell, rstCell, ageCell)
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func anyPodMetrics(pods []k8s.PodInfo) bool {
	for _, p := range pods {
		if p.CPU > 0 || p.Memory > 0 {
			return true
		}
	}
	return false
}

func (t *Tree) renderContainerRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	cs := t.filteredContainers()
	if len(cs) == 0 {
		return th.Idle.Render("no containers")
	}
	showMetrics := anyContainerMetrics(cs)

	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		stW  = 14
		cpuW = 6
		memW = 7
	)
	colsCount := 3 // K, NAME, STATE
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		colsCount += 2
		usedCpuW, usedMemW = cpuW, memW
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - stW - usedCpuW - usedMemW - gaps
	if nW < 10 {
		nW = 10
	}

	cols := []int{kW, nW, stW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME · IMAGE", "STATE"}
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(cs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ci := cs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameW := nW / 2
		imgW := nW - nameW
		// init/ephemeral containers keep their inline kind prefix on the name
		// cell ([init], [eph]) — the K column carries the consistent
		// "container" mark, and the inline tag carries the sub-kind nuance.
		namePrefix := ""
		if ci.Kind != "regular" {
			namePrefix = th.MetaOn("["+shortKind(ci.Kind)+"] ", bg)
		}
		nameCell := namePrefix +
			th.NameStrongOn(components.TruncPad(ci.Name, nameW-lipgloss.Width(namePrefix)), bg) +
			th.MetaOn(components.TruncPad(" · "+ci.Image, imgW), bg)
		stateCell := th.StatusGlyph(ci.State, stW, bg)

		vals := []string{kindGlyph(k8s.KindContainer, ""), nameCell, stateCell}
		if showMetrics {
			vals = append(vals,
				th.MetaOn(k8s.FormatCPU(ci.CPU), bg),
				th.MetaOn(k8s.FormatMem(ci.Memory), bg),
			)
		}
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func anyContainerMetrics(cs []k8s.ContainerInfo) bool {
	for _, c := range cs {
		if c.CPU > 0 || c.Memory > 0 {
			return true
		}
	}
	return false
}

func (t *Tree) renderPoolRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pools := t.filteredPools()
	if len(pools) == 0 {
		return th.Idle.Render("no node pools")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW    = 1
		hW    = 1
		nodes = 6
		cpuW  = 8
		memW  = 8
		podsW = 9
	)
	gaps := components.GapTotal(7)
	nW := tableW - kW - hW - nodes - cpuW - memW - podsW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, hW, nW, nodes, cpuW, memW, podsW}
	aligns := []components.Align{
		components.AlignLeft, components.AlignLeft, components.AlignLeft,
		components.AlignRight, components.AlignRight, components.AlignRight, components.AlignRight,
	}
	header := components.Header(th, []string{"", "", "POOL", "NODES", "CPU%", "MEM%", "PODS"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pools), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pools[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		health := healthGlyphColored(th, p.Health, bg)
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nW), bg)
		nodes := fmt.Sprintf("%d", p.Nodes)
		cpu := pctOf(p.CPUUsed, p.CPUAlloc)
		mem := pctOf(p.MemUsed, p.MemAlloc)
		pods := fmt.Sprintf("%d/%d", p.PodsCurrent, p.PodsMax)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNodePool, ""), health, nameCell, nodes, cpu, mem, pods},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderNodeRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	nodes := t.filteredNodes()
	if len(nodes) == 0 {
		return th.Idle.Render("no nodes")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		hW   = 1
		stW  = 12
		cpuW = 8
		memW = 8
		podW = 9
	)
	gaps := components.GapTotal(7)
	nW := tableW - kW - hW - stW - cpuW - memW - podW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, hW, nW, stW, cpuW, memW, podW}
	aligns := []components.Align{
		components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft,
		components.AlignRight, components.AlignRight, components.AlignRight,
	}
	header := components.Header(th, []string{"", "", "NAME", "STATUS", "CPU%", "MEM%", "PODS"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(nodes), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		n := nodes[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		health := healthGlyphColored(th, n.Health, bg)
		nameCell := th.NameStrongOn(components.TruncPad(n.Name, nW), bg)
		statusCell := th.StatusGlyph(n.Status, stW, bg)
		cpu := pctOf(n.CPUUsed, n.CPUAlloc)
		mem := pctOf(n.MemUsed, n.MemAlloc)
		pods := fmt.Sprintf("%d/%d", n.PodsCurrent, n.PodsMax)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNode, ""), health, nameCell, statusCell, cpu, mem, pods},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderNamespaceRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	nss := t.filteredNamespaces()
	if len(nss) == 0 {
		return th.Idle.Render("no namespaces")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW  = 1
		agW = 5
	)
	gaps := components.GapTotal(3)
	nW := tableW - kW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW, agW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignRight}
	header := components.Header(th, []string{"", "NAMESPACE", "AGE"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(nss), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ns := nss[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(ns.Name, nW), bg)
		ageCell := th.Age(ns.Age, bg)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNamespace, ""), nameCell, ageCell},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// ── Filtering / sorting ──────────────────────────────────────────────

// filteredContexts returns the kube context list with the active filter
// applied. Sorted alphabetically so muscle memory stays stable across
// launches ("prod-eu is always third").
func (t *Tree) filteredContexts() []config.ContextInfo {
	if t.children == nil {
		return nil
	}
	ctxs := append([]config.ContextInfo(nil), t.children.Contexts...)
	sort.Slice(ctxs, func(i, j int) bool { return ctxs[i].Name < ctxs[j].Name })
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(ctxs))
		for i, c := range ctxs {
			names[i] = c.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]config.ContextInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, ctxs[i])
		}
		ctxs = filtered
	}
	return ctxs
}

// renderContextRows renders the list-of-contexts level. The K-column glyph
// (⎈) doubles as the active-context marker: dim chrome FG when the row is
// just another option, green-bold when the row is the cluster the user is
// currently "in". Shape encodes kind, color encodes activity — same rule
// the rest of the app follows for health.
func (t *Tree) renderContextRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	ctxs := t.filteredContexts()
	if len(ctxs) == 0 {
		return th.Idle.Render("no kube contexts found")
	}

	// Cluster + user columns light up only when they add information beyond
	// the context name (single-cluster setups collapse to one repeated value).
	showCluster, showUser := false, false
	for _, ci := range ctxs {
		if ci.Cluster != "" && ci.Cluster != ci.Name {
			showCluster = true
		}
		if ci.User != "" && ci.User != ci.Name {
			showUser = true
		}
	}

	tableW := innerW - components.EdgeTotal()
	const kW = 1
	colsCount := 2
	cW, uW := 0, 0
	if showCluster {
		colsCount++
		cW = 18
	}
	if showUser {
		colsCount++
		uW = 18
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - cW - uW - gaps
	if nW < 10 {
		nW = 10
	}

	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "CLUSTER"}
	if showCluster {
		cols = append(cols, cW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "CLUSTER-REF")
	}
	if showUser {
		cols = append(cols, uW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "USER")
	}
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(ctxs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ci := ctxs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		// Bake bg into the glyph so the row's selection / stripe doesn't punch
		// through the foreground reset (same trick StatusGlyph uses).
		var kindCell string
		if ci.Name == c.ActiveContext {
			kindCell = bg.Foreground(th.P.Green).Bold(true).Render(kindGlyph(k8s.KindContext, ""))
		} else {
			kindCell = bg.Foreground(th.P.Dim).Render(kindGlyph(k8s.KindContext, ""))
		}
		nameCell := th.NameStrongOn(components.TruncPad(ci.Name, nW), bg)
		vals := []string{kindCell, nameCell}
		if showCluster {
			vals = append(vals, th.MetaOn(components.TruncPad(ci.Cluster, cW), bg))
		}
		if showUser {
			vals = append(vals, th.MetaOn(components.TruncPad(ci.User, uW), bg))
		}
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) filteredApps() []k8s.AppInfo {
	if t.children == nil {
		return nil
	}
	apps := t.children.Apps
	if t.alphaSort {
		apps = sortAppsAlpha(apps)
	}
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(apps))
		for i, a := range apps {
			names[i] = a.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.AppInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, apps[i])
		}
		apps = filtered
	}
	return apps
}

func sortAppsAlpha(in []k8s.AppInfo) []k8s.AppInfo {
	out := append([]k8s.AppInfo(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Tree) filteredPods() []k8s.PodInfo {
	if t.children == nil {
		return nil
	}
	pods := t.children.Pods
	if !t.alphaSort {
		pods = sortPodsHealth(pods)
	}
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pods))
		for i, p := range pods {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.PodInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pods[i])
		}
		pods = filtered
	}
	return pods
}

func sortPodsHealth(in []k8s.PodInfo) []k8s.PodInfo {
	out := append([]k8s.PodInfo(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		hi, hj := k8s.PodHealth(out[i]), k8s.PodHealth(out[j])
		if hi != hj {
			return hi < hj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Tree) filteredContainers() []k8s.ContainerInfo {
	if t.children == nil {
		return nil
	}
	cs := t.children.Containers
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.ContainerInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, cs[i])
		}
		cs = filtered
	}
	return cs
}

func (t *Tree) filteredPools() []k8s.NodePoolInfo {
	if t.children == nil {
		return nil
	}
	pools := t.children.Pools
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pools))
		for i, p := range pools {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NodePoolInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pools[i])
		}
		pools = filtered
	}
	return pools
}

func (t *Tree) filteredNodes() []k8s.NodeInfo {
	if t.children == nil {
		return nil
	}
	nodes := t.children.Nodes
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(nodes))
		for i, n := range nodes {
			names[i] = n.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NodeInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, nodes[i])
		}
		nodes = filtered
	}
	return nodes
}

func (t *Tree) filteredNamespaces() []k8s.NamespaceInfo {
	if t.children == nil {
		return nil
	}
	nss := t.children.Namespaces
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(nss))
		for i, n := range nss {
			names[i] = n.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NamespaceInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, nss[i])
		}
		nss = filtered
	}
	return nss
}

func (t *Tree) firstUnhealthyIndex() int {
	rows := t.visibleRows()
	for i, r := range rows {
		if r.Health < k8s.HealthHealthy {
			return i
		}
	}
	return t.cursor
}

// ── Glyphs ───────────────────────────────────────────────────────────
//
// The glyph system is organized as visual families so the kind of any row
// is recognisable at a glance — the eye learns the family, not 12 unrelated
// shapes. Every glyph is a single-cell BMP character (no emoji), so column
// alignment and breadcrumb width math stay honest across terminals.
//
// Family A — Workloads (diamonds + clock + half-circle)
//   ◇ Deployment   hollow  → "anonymous replicas, replaceable"
//   ◆ StatefulSet  filled  → "identity-bound replicas"   (hollow→filled
//                                                          encodes identity)
//   ◷ CronJob      clock-quadrant → "time-triggered"
//   ◐ Job          half-filled    → "one-shot, mid-run"
//   ▦ DaemonSet    cross-hatched square → "one-per-node, infra-adjacent"
//                                          (intentionally crosses into the
//                                          square family — DaemonSets are
//                                          workloads BUT node-bound, so the
//                                          glyph carries that duality)
//
// Family B — Infrastructure (squares, hierarchically zoomed)
//   ▣ ClusterFold  square-in-square  → "the whole cluster — frame holding units"
//   ▥ NodePool     vertical-fill     → "a rack: many vertical bars"
//   □ Node         single empty box  → "one unit"
//   ⊟ SystemFold   collapsed         → "hidden / aggregated"
//
// Family C — Scopes & runtime (distinct, single-purpose)
//   ⌂ ContextList  home              → "root / picker"
//   ⎈ Context      Kubernetes helm   → "a cluster" (real k8s mark)
//   ▢ Namespace    rounded square    → "logical scope" (visually distinct
//                                       from family B's sharp squares)
//   ◉ Pod          fisheye circle    → "an instance with internal state"
//                                       (distinct from `●` so the pod kind
//                                       glyph doesn't clash with the colored
//                                       status dot in the STATUS column)
//   ▾ Container    small triangle    → "drill-disclosure: a piece inside"
//
// Indicator (not a kind):
//   ↕ HPA badge — vertical scale arrows on App rows

func appKindGlyph(kind string) string {
	switch kind {
	case k8s.AppKindDeployment:
		return "◇"
	case k8s.AppKindStatefulSet:
		return "◆"
	case k8s.AppKindDaemonSet:
		return "▦"
	case k8s.AppKindCronJob:
		return "◷"
	case k8s.AppKindJob:
		return "◐"
	}
	return "·"
}

func kindGlyph(kind, sub string) string {
	switch kind {
	case k8s.KindContextList:
		return "⌂"
	case k8s.KindContext:
		return "⎈"
	case k8s.KindApp:
		return appKindGlyph(sub)
	case k8s.KindPod:
		return "◉"
	case k8s.KindContainer:
		return "▾"
	case k8s.KindClusterFold:
		return "▣"
	case k8s.KindNodePool:
		return "▥"
	case k8s.KindNode:
		return "□"
	case k8s.KindNamespace:
		return "▢"
	case k8s.KindSystemFold:
		return "⊟"
	case k8s.KindHPA:
		return "↕"
	}
	return ""
}

func healthGlyphColored(t *theme.Theme, h k8s.Health, bg lipgloss.Style) string {
	g := h.Glyph()
	switch h {
	case k8s.HealthHealthy:
		return bg.Foreground(t.P.Green).Bold(true).Render(g)
	case k8s.HealthDegraded:
		return bg.Foreground(t.P.Yellow).Bold(true).Render(g)
	case k8s.HealthFailing:
		return bg.Foreground(t.P.Red).Bold(true).Render(g)
	case k8s.HealthInProgress:
		return bg.Foreground(t.P.Blue).Bold(true).Render(g)
	}
	return bg.Foreground(t.P.Dim).Render(g)
}

func shortKind(kind string) string {
	switch kind {
	case "regular":
		return "reg"
	case "init":
		return "init"
	case "ephemeral":
		return "eph"
	}
	return "—"
}

func clonePath(p []NodeRef) []NodeRef {
	out := make([]NodeRef, len(p))
	copy(out, p)
	return out
}
