package tui

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

// Single-pane navigation. Every screen is one page: the Harbour (cluster
// picker), a list (a place: the cluster, a namespace, Workloads…), a
// resource page (a thing: an app, a pod…) or a tool page (chat, hot pods,
// forwards, drain). The tree's path is the hierarchy; Enter opens the
// highlighted row, ← → walk the tabs (← on the first tab steps out), and
// Esc leaves the page in one press:
//
//  1. a tool page closes, uncovering the page beneath;
//  2. after a search or Connected jump, the page the jump started from;
//  3. otherwise the parent page.
//
// The footer's `esc ‹ X` names exactly that destination (backLabel).

// pageMemo is a resource page's tabs, kept while the operator is deeper in
// or elsewhere, so coming back finds the page as it was left.
type pageMemo struct {
	tab, entry sections.WorkspaceTab
}

// jump is a search or Connected jump: where it started, and where it landed
// once the destination loaded. Esc on the landing page returns to the start.
type jump struct {
	from   []sections.NodeRef
	memo   pageMemo
	landed []sections.NodeRef
}

// maxJumps bounds the jump history; older starts are forgotten first.
const maxJumps = 128

// stateReadingTab is the reading tab a state drives, if any.
func stateReadingTab(s state) (sections.WorkspaceTab, bool) {
	//exhaustive:ignore
	switch s {
	case stateMetricsView:
		return sections.WSTabMetrics, true
	case stateLogView:
		return sections.WSTabLogs, true
	case stateEventsView:
		return sections.WSTabEvents, true
	case stateDescribeView:
		return sections.WSTabYaml, true
	default:
		return 0, false
	}
}

// isToolState reports whether the state is a tool page: a full page that
// covers whatever page it was opened from and returns to it on Esc.
func isToolState(s state) bool {
	//exhaustive:ignore
	switch s {
	case stateChatView, stateHotView, stateForwardsList, stateDrainProgress:
		return true
	default:
		return false
	}
}

// pageAvailability lists the tabs a resource page offers for ref.
func pageAvailability(ref sections.NodeRef) sections.WSTabAvailability {
	av := sections.WSTabAvailability{
		Children: sections.ChildrenLabel(ref.Kind) != "",
		Metrics:  sections.MetricsSupported(ref),
	}
	//exhaustive:ignore
	switch ref.Kind {
	case k8s.KindGenericResource:
		av.Events, av.Yaml = ref.SubKind != "", !ref.GVR.Empty()
	case k8s.KindPod, k8s.KindContainer, k8s.KindApp:
		av.Logs, av.Events, av.Yaml = true, true, true
	case k8s.KindNode, k8s.KindService, k8s.KindIngress, k8s.KindPVC,
		k8s.KindConfigMap, k8s.KindSecret, k8s.KindPV:
		av.Events, av.Yaml = true, true
	}
	return av
}

// defaultTabFor is the tab a resource page opens on: what the resource
// holds when it holds something, otherwise what it is.
func defaultTabFor(kind string) sections.WorkspaceTab {
	if sections.ChildrenLabel(kind) != "" {
		return sections.WSTabChildren
	}
	return sections.WSTabDetails
}

// pageTabs is the current resource page's tab strip.
func (m *Model) pageTabs() []sections.WorkspaceTab {
	return sections.AvailableTabs(pageAvailability(m.tree.Focus()))
}

// settlePage brings pageTab in step with the state that drives a reading
// tab, then syncs the tree's subject. It runs before every update and
// render, between transitions, so verbs and the Inspector always agree
// with the screen.
func (m *Model) settlePage() {
	base := m.baseState()
	if m.modal && !m.isModalState(base) {
		m.modal = false
	}
	if tab, ok := stateReadingTab(base); ok && !m.modal {
		// A view in the modal floats over the list; the page keeps its tab.
		m.pageTab = tab
	} else if !m.modal && base == stateMain && m.pageTab.IsReading() {
		// The reading view closed under us (a confirmation finished, a
		// stream ended): fall back to the tab the page was opened on.
		m.pageTab = m.entryTab
		if m.pageTab.IsReading() {
			m.pageTab = defaultTabFor(m.tree.Focus().Kind)
		}
	}
	m.syncPageSubject()
}

// syncPageSubject points the tree's selection at the page's own resource
// whenever a resource page shows anything but its children. Safe to call
// mid-transition: it never changes which tab is open.
func (m *Model) syncPageSubject() {
	if m.pageMemo == nil {
		m.pageMemo = make(map[string]pageMemo)
	}
	if !m.tree.OnResourcePage() {
		m.tree.PageSubject = false
		return
	}
	tabs := m.pageTabs()
	if !slices.Contains(tabs, m.pageTab) {
		m.pageTab = defaultTabFor(m.tree.Focus().Kind)
	}
	if !slices.Contains(tabs, m.entryTab) {
		m.entryTab = defaultTabFor(m.tree.Focus().Kind)
	}
	m.tree.PageSubject = m.pageTab != sections.WSTabChildren
}

// showTab switches the current page's tab without adding history. A
// reading tab is (re)opened through the same verb path its key takes, so
// loading and streaming live in one place.
func (m *Model) showTab(tab sections.WorkspaceTab) tea.Cmd {
	m.cleanupImmersiveState()
	m.state = stateMain
	m.pageTab = tab
	m.syncPageSubject()
	m.tree.Focused = true
	if !tab.IsReading() {
		return m.syncSelection()
	}
	var cmd tea.Cmd
	if tab == sections.WSTabMetrics {
		m.state = stateMetricsView
		cmd = m.syncMetrics(true)
	} else if tab == sections.WSTabEvents {
		cmd, _ = m.openEvents(m.tree.SelectedRef())
	} else if synth, ok := tabActivationKey(tab); ok {
		cmd, _ = m.handleTreeAction(synth)
	}
	m.syncPageSubject()
	return cmd
}

// memoPage remembers the current resource page's tabs before leaving it.
func (m *Model) memoPage() {
	if m.pageMemo == nil {
		m.pageMemo = make(map[string]pageMemo)
	}
	if m.tree.OnResourcePage() {
		m.pageMemo[sections.PathKey(m.tree.Path())] = pageMemo{tab: m.pageTab, entry: m.entryTab}
	}
}

// restorePage shows the page now in focus as it was left, or on its
// default tab the first time.
func (m *Model) restorePage() tea.Cmd {
	if !m.tree.OnResourcePage() {
		m.pageTab, m.entryTab = sections.WSTabDetails, sections.WSTabDetails
		m.syncPageSubject()
		return nil
	}
	key := sections.PathKey(m.tree.Path())
	memo, ok := m.pageMemo[key]
	delete(m.pageMemo, key)
	if !ok {
		d := defaultTabFor(m.tree.Focus().Kind)
		memo = pageMemo{tab: d, entry: d}
	}
	m.entryTab = memo.entry
	return m.showTab(memo.tab)
}

// openRow opens the highlighted row: a place becomes the list page, a
// thing its resource page on the default tab (Enter and →).
func (m *Model) openRow() tea.Cmd {
	if m.tree.PageSubject {
		return nil
	}
	ref := m.tree.SelectedRef()
	if ref.Kind == "" {
		return nil
	}
	if ref.Kind == k8s.KindResourceBrowser {
		return m.tree.Push(ref) // opens the browser overlay; the path stays
	}
	if sections.IsResourceKind(ref.Kind) && sections.ChildrenLabel(ref.Kind) == "" {
		// A thing that holds nothing has no list to open: Enter describes
		// it in the modal, as d does.
		return m.openSheet(ref)
	}
	m.memoPage()
	push := m.tree.Push(ref)
	m.pageTab, m.entryTab = sections.WSTabChildren, sections.WSTabChildren
	if !sections.IsResourceKind(ref.Kind) {
		m.pageTab, m.entryTab = sections.WSTabDetails, sections.WSTabDetails
	}
	m.syncPageSubject()
	return tea.Batch(push, m.syncSelection())
}

// beginJump records the page a search or Connected jump starts from.
func (m *Model) beginJump() {
	m.memoPage()
	m.jumps = append(m.jumps, jump{
		from: append([]sections.NodeRef(nil), m.tree.Path()...),
		memo: pageMemo{tab: m.pageTab, entry: m.entryTab},
	})
	if len(m.jumps) > maxJumps {
		m.jumps = slices.Delete(m.jumps, 0, len(m.jumps)-maxJumps)
	}
}

// landJump stamps the pending jump with where it arrived.
func (m *Model) landJump() {
	if n := len(m.jumps); n > 0 && m.jumps[n-1].landed == nil {
		m.jumps[n-1].landed = append([]sections.NodeRef(nil), m.tree.Path()...)
	}
}

// liveJump returns the jump Esc would undo from the current page. A jump
// stays live while the operator is on its landing page or below it.
func (m *Model) liveJump() (jump, bool) {
	path := m.tree.Path()
	for i := len(m.jumps) - 1; i >= 0; i-- {
		j := m.jumps[i]
		if j.landed == nil {
			return jump{}, false
		}
		if len(path) >= len(j.landed) && slices.Equal(path[:len(j.landed)], j.landed) {
			return j, slices.Equal(path, j.landed)
		}
	}
	return jump{}, false
}

// pruneJumps forgets jumps the operator has since navigated away from.
func (m *Model) pruneJumps() {
	path := m.tree.Path()
	for n := len(m.jumps); n > 0; n = len(m.jumps) {
		j := m.jumps[n-1]
		if j.landed == nil || (len(path) >= len(j.landed) && slices.Equal(path[:len(j.landed)], j.landed)) {
			return
		}
		m.jumps = m.jumps[:n-1]
	}
}

// goBack leaves the current page (Esc, and ← from a first tab or a list).
// See the rules above.
func (m *Model) goBack() (tea.Model, tea.Cmd) {
	if isToolState(m.state) {
		m.cleanupImmersiveState()
		m.state = stateMain
		if m.tree.OnResourcePage() && m.pageTab.IsReading() {
			return m, m.showTab(m.pageTab)
		}
		m.syncPageSubject()
		return m, m.syncSelection()
	}
	// A jump still loading its destination is cancelled outright.
	pending := len(m.jumps) > 0 && m.jumps[len(m.jumps)-1].landed == nil
	if pending {
		m.paletteTarget, m.paletteAction = nil, ""
	}
	m.pruneJumps()
	if j, onLanding := m.liveJump(); onLanding || pending {
		if pending {
			j = m.jumps[len(m.jumps)-1]
		}
		m.jumps = m.jumps[:len(m.jumps)-1]
		m.cleanupImmersiveState()
		m.state = stateMain
		nav := m.tree.NavigateTo(j.from)
		if m.tree.OnResourcePage() {
			m.pageMemo[sections.PathKey(j.from)] = j.memo
		}
		return m, tea.Batch(nav, m.restorePage())
	}
	if len(m.tree.Path()) <= 1 {
		return m, nil // the Harbour is the root
	}
	m.cleanupImmersiveState()
	m.state = stateMain
	pop := m.tree.Pop()
	return m, tea.Batch(pop, m.restorePage())
}

// pageName is how the footer and back hints name a page.
func pageName(ref sections.NodeRef) string {
	if ref.Kind == k8s.KindContextList || ref.Kind == "" {
		return "Clusters"
	}
	name := ref.Display
	if name == "" {
		name = ref.Name
	}
	// Fold displays carry a count ("Workloads (25)"); a name doesn't need it.
	if i := strings.LastIndex(name, " ("); i > 0 && strings.HasSuffix(name, ")") {
		if _, err := strconv.Atoi(name[i+2 : len(name)-1]); err == nil {
			name = name[:i]
		}
	}
	return name
}

// backLabel names the page Esc returns to, or "" at the Harbour.
func (m *Model) backLabel() string {
	if isToolState(m.baseState()) {
		if m.tree.OnResourcePage() && m.pageTab.IsReading() {
			return m.pageTab.Title(m.tree.Focus().Kind)
		}
		return pageName(m.tree.Focus())
	}
	if n := len(m.jumps); n > 0 && m.jumps[n-1].landed == nil && len(m.jumps[n-1].from) > 0 {
		from := m.jumps[n-1].from
		return pageName(from[len(from)-1])
	}
	if j, onLanding := m.liveJump(); onLanding && len(j.from) > 0 {
		return pageName(j.from[len(j.from)-1])
	}
	if len(m.tree.Path()) <= 1 {
		return ""
	}
	return pageName(m.tree.Parent())
}

// pageParentMsg is a refreshed parent list for the resource page on screen.
type pageParentMsg struct{ sections.TreeChildrenLoadedMsg }

// loadPageParent refreshes the list the current resource page came from,
// so the header's health, readiness and sparklines stay live. Reads go
// through the informer cache like every other tree load.
func (m *Model) loadPageParent() tea.Cmd {
	parent := m.tree.ParentPath()
	if !m.tree.OnResourcePage() || len(parent) == 0 || m.ctx.Manager == nil {
		return nil
	}
	var userNS, sysNS []string
	if m.ctx.AppCfg != nil {
		userNS, sysNS = m.ctx.AppCfg.UserNamespaces, m.ctx.AppCfg.SystemNamespaces
	}
	load := sections.LoadTreeChildren(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, parent, userNS, sysNS)
	return func() tea.Msg {
		msg, ok := load().(sections.TreeChildrenLoadedMsg)
		if !ok {
			return nil
		}
		return pageParentMsg{msg}
	}
}
