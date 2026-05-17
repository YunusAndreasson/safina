package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/claude"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/sections"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

const refreshInterval = 5

// State enumerates the modal layers the root manages on top of the main view.
type state int

const (
	stateMain state = iota
	// stateLogView / stateEventsView / stateDescribeView / stateHotView /
	// stateForwardsList / stateDrainProgress are kept as drivers of *which*
	// workspace tab is active. In the cosmic-horizon redesign these are no
	// longer full-screen modals — they render as tabs inside the workspace
	// pane while the tree stays visible. Update paths still flip state to
	// drive the tab activation; renderMain consults the state and selects
	// the matching WorkspaceTab.
	stateLogView
	stateEventsView
	stateDescribeView
	stateHelp
	stateScaleInput
	stateDeleteConfirm
	stateRollbackConfirm
	stateRestartConfirm
	// stateRestartPodConfirm is the y/n gate when the user pressed R on a
	// Pod row. Mechanically a delete (the parent controller recreates the
	// pod), but the modal title + success banner read "restart" — same verb
	// the keymap and footer taught.
	stateRestartPodConfirm
	stateShellView
	stateEditConfirm
	statePalette
	statePortForwardInput
	stateForwardsList
	stateCordonConfirm
	stateDrainConfirm
	stateDrainProgress
	// stateHotView is the noisy-neighbours view: top-N pods cluster-wide by
	// CPU usage. Renders as a pinned workspace tab while active.
	stateHotView
	// stateBrowser is the Resource Browser modal — toggles tree folds.
	stateBrowser
	// stateClusters is the Ctrl+K cluster-switcher overlay. Memory rule:
	// no auto-default at startup; mid-session switching is opt-in via
	// this overlay (jumps to the same load path the tree's root drill uses).
	stateClusters
	// stateChatView is the workspace's Chat tab being active. The chat
	// textarea captures keystrokes while we're in this state; pressing Esc
	// (or `1`) switches back to the Inspector tab — same affordance as
	// closing any other immersive tab.
	stateChatView
)

// editSession holds the in-flight YAML-edit lifecycle: the original bytes,
// the user's edited bytes, and the resource identity so the apply step can
// be issued without re-resolving from the (possibly re-scrolled) tree.
type editSession struct {
	Kind, Namespace, Name string
	TempPath              string
	Orig, Edited          []byte
}

// LineDiffStat returns a "+A −B" summary for the confirm modal. Counts are
// the symmetric difference of the two line multisets — line moves count as
// both an add and a remove, which matches what an operator expects from a
// text-level "what changed" prompt.
func (s *editSession) LineDiffStat() (added, removed int) {
	if s == nil {
		return 0, 0
	}
	count := func(b []byte) map[string]int {
		m := make(map[string]int)
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			m[l]++
		}
		return m
	}
	a := count(s.Orig)
	bn := count(s.Edited)
	for line, c := range bn {
		if d := c - a[line]; d > 0 {
			added += d
		}
	}
	for line, c := range a {
		if d := c - bn[line]; d > 0 {
			removed += d
		}
	}
	return added, removed
}

// Model is the root Bubble Tea model. It composes the feature sections and
// owns the modal/state machine on top of them. The contexts panel is gone:
// kube contexts now live as the tree's root level, so the user "drills in"
// to a cluster the same way they drill into a deployment, and "esc"s their
// way back out.
type Model struct {
	ctx *tuictx.Ctx

	tree      sections.Tree
	chat      sections.Chat
	logs      sections.Logs
	events    sections.Events
	describe  sections.Describe
	shell     sections.Shell
	palette   sections.Palette
	browser   sections.Browser
	forwards  sections.Forwards
	drain     sections.Drain
	inspector sections.Inspector
	activity  sections.Activity
	connected sections.Connected
	hot       sections.Hot
	clusters  sections.ClusterSwitcher

	// allContexts caches the kube-context list for the Ctrl+K switcher.
	// Seeded by SetContexts at startup.
	allContexts []config.ContextInfo

	// discoveryByContext caches whether ListAvailableResources has
	// already populated the tree's discovery cache for a given context.
	// Reset on every NewModel; entries cleared on cluster switch in the
	// TreeFocusChangedMsg handler so the next entry refetches.
	discoveryByContext map[string]bool

	// claudeSvc is the long-lived bridge to the user's `claude` CLI. One
	// per safina process; subsequent turns resume the same conversation.
	claudeSvc *claude.Service

	// chatStreamCh is the active turn's event channel, set by
	// ChatStreamStartedMsg and read once per ChatTokenMsg / ChatRetryMsg.
	chatStreamCh <-chan claude.Event

	state state

	help       help.Model
	scaleInput textinput.Model
	// pfInput collects the "L:R" pair for a new port-forward. Reused across
	// every start; cleared on each open.
	pfInput textinput.Model
	// pfTarget remembers the (kind, ns, name) the user pressed `f` on so the
	// confirm-enter path doesn't re-query the tree (the tree cursor may have
	// moved before the user finishes typing).
	pfTarget struct {
		Kind, Namespace, Name string
	}

	// nodeTarget pins the node the user pressed C/D on, so the confirm
	// flow doesn't have to re-read the tree cursor (which may have moved
	// while the operator hesitated over the y/n prompt).
	nodeTarget struct {
		Name        string
		Schedulable bool // captured at keypress; drives the C-toggle's verb
	}

	// drainConfirmPods is the pod count shown in the drain confirm modal
	// ("will evict N pods"). -1 means "still loading"; 0+ means landed.
	drainConfirmPods int

	// drainCh holds the running drain's event channel between
	// DrainStartedMsg and the final Done event so successive
	// NextDrainEvent reads target the same chan.
	drainCh sections.DrainStream

	// banner is the transient one-line message rendered above the main
	// split — errors in red, action confirmations in green. Both share the
	// same slot (newer overwrites older); bannerEpoch is the single-flight
	// token that lets a still-pending clear timer for an older banner
	// no-op itself instead of wiping the live one.
	bannerMsg   string
	bannerErr   bool
	bannerEpoch uint64

	logCh chan k8s.PodLogEvent

	shellSession *k8s.ShellSession

	// treeLoadCancel cancels an in-flight tree-children load when a new
	// drill or pop arrives, so rapid navigation doesn't queue stale data.
	treeLoadCancel context.CancelFunc

	// now is the wall-clock time displayed in the topbar; updated every
	// second by clockMsg ticks. Carrying it on the model means re-renders
	// triggered by anything else (a key press, a refresh tick) still show
	// the current time without waiting for the next second to roll over.
	now time.Time

	// lastRefresh is when we last successfully loaded children for the
	// current cluster view. The topbar shows "↻ Ns" off this so the user
	// can tell at a glance whether the data on screen is live or stale.
	// Zero-value while at the picker or before the first load lands.
	lastRefresh time.Time

	// clusterHealth is the most recent snapshot rendered into the topbar
	// pill. Updated by clusterHealthMsg on a slower cadence than the tree
	// refresh (the data sources are cluster-scoped list calls, not per-row
	// reads). HasData() returns false until the first successful load lands.
	clusterHealth k8s.ClusterHealth
	// clusterHealthAt is when clusterHealth was last successfully refreshed —
	// used to throttle the next fetch, and to drop the snapshot when the
	// user switches clusters so a stale value from cluster A doesn't render
	// over cluster B.
	clusterHealthAt time.Time
	// clusterHealthCtx is the context name clusterHealth was loaded against;
	// reset to "" on context switch so the pill blanks until the new
	// cluster's first fetch lands.
	clusterHealthCtx string

	// title is the terminal-tab title rendered declaratively via tea.View.
	// Updated whenever the active cluster changes so on-callers running
	// many tabs can identify which cluster a tab is pointed at.
	title string

	// focused tracks whether the terminal currently has OS focus. The
	// refresh and clock ticks short-circuit while this is false so we
	// don't burn API quota loading data the operator isn't looking at —
	// FocusMsg / BlurMsg in update.go flip it. Starts true because a
	// program that never sees a focus report (xterm without focus events)
	// must still refresh; tea.View.ReportFocus opts us in to the reports.
	focused bool

	// lightBgWarned latches the one-shot banner raised when
	// BackgroundColorMsg reports a light terminal. TokyoNight is designed
	// for dark backgrounds — on a light terminal it reads as a low-contrast
	// gray wash. We don't auto-switch palettes (no light palette ships
	// today), but we tell the operator once so they understand the muddy
	// rendering isn't a bug.
	lightBgWarned bool

	// tabStripOriginX / tabStripOriginY / tabHitboxes are stashed by the
	// View pass so the MouseClickMsg handler can map a click column on the
	// workspace tab strip back to the WorkspaceTab the user wants to
	// activate. Mirrors chat.SetScreenOrigin: rendering knows the geometry
	// the click handler needs to consume.
	tabStripOriginX int
	tabStripOriginY int
	tabHitboxes     []sections.TabHitbox

	// peekWorkspace tracks whether Tab has moved focus to the right pane
	// without activating an immersive tab. The active view doesn't change
	// (Inspector stays Inspector), but the gradient border migrates to the
	// workspace and ←/→ start cycling the tab strip. Cleared by another Tab
	// (focus back to tree) or by activating an immersive tab via hotkey.
	peekWorkspace bool

	// startupBanner is a one-shot error message raised in Init(), used by
	// main to surface CLI-flag mistakes (e.g. --context=does-not-exist)
	// without spilling a stderr line into the user's scrollback.
	startupBanner string

	// edit holds the active YAML-edit session between $EDITOR exit and the
	// y/n confirm. nil at all other times. Cleanup (tempfile unlink) is the
	// model's responsibility on every exit path.
	edit *editSession
}

// NewModel constructs the root model. Caller must follow up with SetContexts
// to populate the tree's root level before running the program.
func NewModel(mgr *k8s.Manager, cfg *config.AppConfig) Model {
	t := theme.Default()
	km := keys.Default()
	c := tuictx.New(t, km, mgr, cfg)

	// We deliberately do NOT seed ActiveContext from cfg.LastCluster: the
	// app forces an explicit cluster pick on every launch (no defaults, no
	// muscle-memory shortcuts). LastCluster is still persisted so it could
	// power a future "recent clusters" view, but it's not used here.

	scale := textinput.New()
	scale.Placeholder = "replicas"
	scale.CharLimit = 4
	scale.Prompt = "› "
	scaleStyles := scale.Styles()
	prompt := lipgloss.NewStyle().Foreground(t.P.Pink).Bold(true)
	text := lipgloss.NewStyle().Foreground(t.P.Green).Bold(true)
	scaleStyles.Focused.Prompt = prompt
	scaleStyles.Focused.Text = text
	scaleStyles.Focused.Placeholder = t.DimTxt
	scaleStyles.Blurred.Prompt = prompt
	scaleStyles.Blurred.Text = text
	scaleStyles.Blurred.Placeholder = t.DimTxt
	scale.SetStyles(scaleStyles)

	pf := textinput.New()
	pf.Placeholder = "8080:80"
	pf.CharLimit = 12
	pf.Prompt = "› "
	pfStyles := pf.Styles()
	pfStyles.Focused.Prompt = prompt
	pfStyles.Focused.Text = text
	pfStyles.Focused.Placeholder = t.DimTxt
	pfStyles.Blurred.Prompt = prompt
	pfStyles.Blurred.Text = text
	pfStyles.Blurred.Placeholder = t.DimTxt
	pf.SetStyles(pfStyles)

	hp := help.New()
	hp.Styles.ShortKey = t.HelpKey
	hp.Styles.ShortDesc = t.HelpDesc
	hp.Styles.FullKey = t.HelpKey
	hp.Styles.FullDesc = t.HelpDesc
	sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim)
	hp.Styles.ShortSeparator = sepStyle
	hp.Styles.FullSeparator = sepStyle
	hp.ShortSeparator = " · "
	hp.FullSeparator = "   "

	svc := claude.NewService()

	m := Model{
		ctx:                c,
		tree:               sections.NewTree(c),
		chat:               sections.NewChat(c, svc),
		logs:               sections.NewLogs(c),
		events:             sections.NewEvents(c),
		describe:           sections.NewDescribe(c),
		shell:              sections.NewShell(c),
		palette:            sections.NewPalette(),
		browser:            sections.NewBrowser(),
		forwards:           sections.NewForwards(c),
		drain:              sections.NewDrain(c),
		inspector:          sections.NewInspector(),
		activity:           sections.NewActivity(),
		connected:          sections.NewConnected(),
		hot:                sections.NewHot(),
		clusters:           sections.NewClusterSwitcher(),
		claudeSvc:          svc,
		state:              stateMain,
		help:               hp,
		scaleInput:         scale,
		pfInput:            pf,
		now:                time.Now(),
		title:              "safina",
		discoveryByContext: make(map[string]bool),
		// Default to focused: terminals that don't report focus events leave
		// the field at true forever and behave exactly like v1 — every tick
		// refreshes. Terminals that do report focus override on the first
		// FocusMsg / BlurMsg (BlurMsg fires immediately at startup if the
		// terminal isn't focused at launch).
		focused: true,
	}
	m.tree.Focused = true
	if cfg != nil {
		m.tree.SetEnabledResources(cfg.EnabledResources)
	}
	return m
}

// syncChatContext pushes the latest tree selection into the chat panel so
// the strip + chips reflect "now". Cheap to call on every key press —
// rendering the strip is O(path-length) and we don't rebuild the prompt
// payload until the user actually sends. Does NOT touch the right-stack
// tiles; callers that need those should use syncSelection.
func (m *Model) syncChatContext() {
	snap := sections.BuildContextSnapshot(&m.tree, m.ctx.ActiveContext)
	suggested := []string{}
	// The chat shows starter chips only at the cluster picker (no resource
	// yet) and on unhealthy selections — we centralise that gate in
	// chat_suggestions.go so the chat doesn't need to know k8s health.
	suggested = append(suggested, sections.ChatSuggestionsForRef(m.tree.SelectedRef())...)
	m.chat.SetCrumbs(snap.Crumbs, suggested)
}

// syncSelection refreshes everything that tracks the tree cursor: chat
// context strip, Inspector (with optional deep-fetch), Activity (with
// optional events fetch). Returns a batched Cmd the caller plumbs into
// the outer Update return; nil when there's nothing to load.
func (m *Model) syncSelection() tea.Cmd {
	m.syncChatContext()
	// Inspector rebuilds its base snapshot synchronously and may
	// request a kind-specific detail fetch when the selection changed.
	inspectCmd := m.inspector.SetSelection(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, &m.tree)
	connCmd := m.connected.SetSelection(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, &m.tree)

	if m.ctx.ActiveContext == "" || m.ctx.Manager == nil {
		return tea.Batch(inspectCmd, connCmd)
	}
	ref := m.tree.SelectedRef()
	kind, ns, name := activityTarget(ref, &m.tree)
	if kind == "" {
		m.activity.Clear()
		return tea.Batch(inspectCmd, connCmd)
	}
	actCmd := m.activity.SetSelection(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, kind, name, ns)
	return tea.Batch(inspectCmd, actCmd, connCmd)
}

// activityTarget maps a tree NodeRef to the involvedObject triple the
// Events API expects, or returns kind == "" for non-eventable rows
// (folds, contexts, sentinels). For containers we route through the
// parent pod, mirroring the events-modal behaviour.
func activityTarget(ref sections.NodeRef, tr *sections.Tree) (kind, ns, name string) {
	switch ref.Kind {
	case k8s.KindApp:
		return ref.SubKind, ref.Namespace, ref.Name
	case k8s.KindPod:
		return "Pod", ref.Namespace, ref.Name
	case k8s.KindContainer:
		pod := tr.Focus()
		return "Pod", pod.Namespace, pod.Name
	case k8s.KindNode:
		return "Node", "", ref.Name
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return "Namespace", "", ref.Name
	case k8s.KindService:
		return "Service", ref.Namespace, ref.Name
	case k8s.KindIngress:
		return "Ingress", ref.Namespace, ref.Name
	case k8s.KindPVC:
		return "PersistentVolumeClaim", ref.Namespace, ref.Name
	case k8s.KindPV:
		return "PersistentVolume", "", ref.Name
	case k8s.KindConfigMap:
		return "ConfigMap", ref.Namespace, ref.Name
	case k8s.KindSecret:
		return "Secret", ref.Namespace, ref.Name
	}
	return "", "", ""
}

// SetContexts seeds the tree's root level with the kube contexts list. The
// app always starts at the picker on row 0 — every launch is an explicit
// "which cluster am I working on right now?" with no defaults, no last-used
// hints, and no muscle-memory shortcuts. We also cache the list on the model
// so the Ctrl+K cluster-switcher overlay can read it without going through
// the tree.
func (m *Model) SetContexts(items []config.ContextInfo) {
	m.tree.SeedContexts(items)
	m.allContexts = items
}

// SetActiveContext is the CLI hook (--context). It just sets the active
// context; the actual drill-in happens in Init() once the tree is seeded.
func (m *Model) SetActiveContext(name string) {
	m.ctx.ActiveContext = name
}

// SetActiveNamespace stores the namespace passed via --namespace.
func (m *Model) SetActiveNamespace(ns string) {
	m.ctx.ActiveNamespace = ns
}

// SetStartupBanner queues a one-shot error banner to be raised when the TUI
// initializes. Used by main to surface invalid CLI flags at first paint
// instead of writing to stderr (which alt-screen would obscure).
func (m *Model) SetStartupBanner(msg string) {
	m.startupBanner = msg
}

func (m *Model) saveConfig() {
	// Preserve the rest of the config (EnabledResources, namespace
	// overrides) — saveConfig is called from every quit path, so a
	// fresh struct here would silently wipe user customisation.
	cfg := m.ctx.AppCfg
	if cfg == nil {
		cfg = &config.AppConfig{}
	}
	cfg.LastCluster = m.ctx.ActiveContext
	cfg.LastNamespace = m.ctx.ActiveNamespace
	_ = config.Save(cfg)
	// Forwards live in user space (a local TCP listener); they don't survive
	// the process anyway, but we close them explicitly so the topbar pill
	// can't lie about active count if anything else inspects the manager
	// between Quit and exit.
	if m.ctx.Manager != nil && m.ctx.Manager.Forwards != nil {
		m.ctx.Manager.Forwards.StopAll()
	}
}

// Init satisfies tea.Model. Kicks off the auto-refresh tick, the wall-clock
// tick, and the spinner. The app always starts at the context picker — the
// user explicitly chooses which cluster to enter on every launch.
//
// RequestBackgroundColor asks the terminal what its background is so we can
// warn (once) when safina is loaded in a light-mode terminal — the TokyoNight
// palette is designed for dark and reads as gray-on-gray otherwise. Bubble
// Tea v2 also auto-sends ColorProfileMsg at startup so the renderer
// downsamples truecolor hex values to ANSI256/ANSI16 on limited terminals;
// we just listen for it and stash the profile for diagnostics.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.autoRefreshTick(),
		m.clockTick(),
		m.ctx.Spinner.Tick,
		tea.RequestBackgroundColor,
	}
	// If kubeconfig contained contexts that couldn't be built into a Client
	// at startup, raise a one-shot banner so the user understands why their
	// picker is shorter than expected. We only show the count + the first
	// name to keep the banner readable; the operator can rerun with `--v=4`
	// or check kubeconfig if they need the full list.
	if skipped := m.ctx.Manager.Skipped; len(skipped) > 0 {
		cmds = append(cmds, m.raiseError(formatSkippedContexts(skipped)))
	}
	// CLI-flag warning takes precedence — it's the most actionable thing the
	// user can address ("oh, I typo'd the context name") and bannerEpoch
	// guarantees only the most recent banner stays visible.
	if m.startupBanner != "" {
		cmds = append(cmds, m.raiseError(m.startupBanner))
		m.startupBanner = ""
	}
	return tea.Batch(cmds...)
}

// formatSkippedContexts builds the one-line banner shown at startup when one
// or more kubeconfig contexts failed to materialise into a Client. Keeps the
// message short — the goal is breadcrumb, not a full diagnostic.
func formatSkippedContexts(skipped []k8s.SkippedContext) string {
	if len(skipped) == 1 {
		return "skipped context " + skipped[0].Name + ": " + skipped[0].Err.Error()
	}
	return "skipped " + plural(len(skipped), "context") + ": " + skipped[0].Name + " and " + plural(len(skipped)-1, "other")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// loadTreeFocus issues a children fetch for the current tree focus,
// cancelling any in-flight load. Rooted at m.ctx.Background so a context
// switch (which calls ResetBackground) cancels everything.
func (m *Model) loadTreeFocus() tea.Cmd {
	if m.treeLoadCancel != nil {
		m.treeLoadCancel()
	}
	cctx, cancel := context.WithCancel(m.ctx.Background)
	m.treeLoadCancel = cancel
	var userNS, sysNS []string
	if m.ctx.AppCfg != nil {
		userNS = m.ctx.AppCfg.UserNamespaces
		sysNS = m.ctx.AppCfg.SystemNamespaces
	}
	return sections.LoadTreeChildren(cctx, m.ctx.Manager, m.ctx.ActiveContext, m.tree.Path(), userNS, sysNS)
}

// clusterHealthRefreshInterval is the throttle between cluster-health
// fetches. The pill data is cluster-scoped (lists every node and every
// pod) — heavier than per-row reads, so we refresh on a less aggressive
// cadence than the 5s tree tick. 15s mirrors the activity tile's own
// throttle and is fast enough to feel "live" without hammering the API
// when the operator is just sitting on a screen.
const clusterHealthRefreshInterval = 15 * time.Second

// loadClusterHealth issues a cluster-health fetch for the active context.
// The returned command's result is consumed by clusterHealthMsg in the
// update loop. Cancellation rides on m.ctx.Background — a context switch
// cancels in-flight loads so a slow API call from a previous cluster
// can't clobber the new cluster's pill.
func (m *Model) loadClusterHealth() tea.Cmd {
	if m.ctx.Manager == nil || m.ctx.ActiveContext == "" {
		return nil
	}
	client, ok := m.ctx.Manager.GetClient(m.ctx.ActiveContext)
	if !ok || client == nil || client.Clientset == nil {
		return nil
	}
	cs := client.Clientset
	bg := m.ctx.Background
	active := m.ctx.ActiveContext
	return func() tea.Msg {
		h, err := k8s.LoadClusterHealth(bg, cs)
		return clusterHealthMsg{Context: active, Health: h, Err: err}
	}
}

// windowTitleFor returns the OS-level terminal window title for the current
// tree path. Operators with several terminal tabs read the title bar to
// identify which cluster (and which resource within it) a tab is pointed at.
// Cluster, then drilled segments — capped so window-list flyouts stay legible.
//
// Truncation strategy: when the full string exceeds max, keep the first
// crumb (cluster — answers "where am I") and the last crumb (leaf —
// answers "looking at what") and ellipsise the middle. A trailing-cut
// would drop the leaf, which is the most specific identifier and the one
// users disambiguate tabs by.
func windowTitleFor(crumbs []components.Crumb) string {
	if len(crumbs) <= 1 {
		// At the cluster picker (or before any drill) there's no useful detail
		// to surface beyond the app name itself.
		return "safina"
	}
	const max = 60
	parts := make([]string, 0, len(crumbs))
	for _, c := range crumbs {
		if c.Label != "" {
			parts = append(parts, c.Label)
		}
	}
	full := "safina · " + strings.Join(parts, " · ")
	if len(full) <= max {
		return full
	}
	// Two-or-fewer crumbs already cooperate with a trailing cut.
	if len(parts) <= 2 {
		return full[:max-3] + "…"
	}
	first, last := parts[0], parts[len(parts)-1]
	short := "safina · " + first + " · … · " + last
	if len(short) <= max {
		return short
	}
	// Leaf alone is more useful than cluster alone when even the short
	// form overflows (deep cluster names + deep leaf names). Keep the
	// leaf, drop the cluster.
	short = "safina · … · " + last
	if len(short) <= max {
		return short
	}
	// Final fallback: cut the leaf itself. We've done our best to keep
	// something readable.
	return short[:max-3] + "…"
}

// clockTick emits one clockMsg per second.
func (m *Model) clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// Banner TTLs follow a φ progression so dwell time scales with severity in
// a perceptually-natural way. A confirmation is a breadcrumb (read in
// peripheral vision); a warning needs a glance; an error needs reading.
// Ratios 1 : φ : φ² ≈ 1 : 1.618 : 2.618 anchored at 1.5s.
const (
	bannerStatusTTL = 1500 * time.Millisecond
	bannerWarnTTL   = 2425 * time.Millisecond // 1.5s × φ
	bannerErrorTTL  = 3925 * time.Millisecond // 1.5s × φ²
)

// raiseBanner sets a new banner and schedules its dismissal. Bumps the epoch
// so any in-flight clear timer for a prior banner becomes a no-op when it
// fires, preventing stacked timers from prematurely wiping newer banners.
func (m *Model) raiseBanner(msg string, isErr bool) tea.Cmd {
	m.bannerEpoch++
	m.bannerMsg = msg
	m.bannerErr = isErr
	epoch := m.bannerEpoch
	ttl := bannerStatusTTL
	if isErr {
		ttl = bannerErrorTTL
		debug.Errf("banner.error", "msg", msg)
	} else {
		debug.Log("banner.status", "msg", msg)
	}
	return tea.Tick(ttl, func(time.Time) tea.Msg { return clearBannerMsg{epoch: epoch} })
}

// raiseError is the error-flavoured raiseBanner — same semantics, red render.
func (m *Model) raiseError(msg string) tea.Cmd { return m.raiseBanner(msg, true) }

// raiseStatus is the success/info-flavoured raiseBanner — green render.
func (m *Model) raiseStatus(msg string) tea.Cmd { return m.raiseBanner(msg, false) }
