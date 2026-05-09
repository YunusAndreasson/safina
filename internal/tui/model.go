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
	stateLogView
	stateEventsView
	stateDescribeView
	stateHelp
	stateScaleInput
	stateDeleteConfirm
	stateRollbackConfirm
	stateRestartConfirm
	stateShellView
)

// Model is the root Bubble Tea model. It composes the feature sections and
// owns the modal/state machine on top of them. The contexts panel is gone:
// kube contexts now live as the tree's root level, so the user "drills in"
// to a cluster the same way they drill into a deployment, and "esc"s their
// way back out.
type Model struct {
	ctx *tuictx.Ctx

	tree     sections.Tree
	chat     sections.Chat
	logs     sections.Logs
	events   sections.Events
	describe sections.Describe
	shell    sections.Shell

	// claudeSvc is the long-lived bridge to the user's `claude` CLI. One
	// per safina process; subsequent turns resume the same conversation.
	claudeSvc *claude.Service

	// chatStreamCh is the active turn's event channel, set by
	// ChatStreamStartedMsg and read once per ChatTokenMsg / ChatRetryMsg.
	chatStreamCh <-chan claude.Event

	state state

	help       help.Model
	scaleInput textinput.Model

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

	// title is the terminal-tab title rendered declaratively via tea.View.
	// Updated whenever the active cluster changes so on-callers running
	// many tabs can identify which cluster a tab is pointed at.
	title string

	// startupBanner is a one-shot error message raised in Init(), used by
	// main to surface CLI-flag mistakes (e.g. --context=does-not-exist)
	// without spilling a stderr line into the user's scrollback.
	startupBanner string
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
		ctx:        c,
		tree:       sections.NewTree(c),
		chat:       sections.NewChat(c, svc),
		logs:       sections.NewLogs(c),
		events:     sections.NewEvents(c),
		describe:   sections.NewDescribe(c),
		shell:      sections.NewShell(c),
		claudeSvc:  svc,
		state:      stateMain,
		help:       hp,
		scaleInput: scale,
		now:        time.Now(),
		title:      "safina",
	}
	m.tree.Focused = true
	return m
}

// syncChatContext pushes the latest tree selection into the chat panel so
// the strip + chips reflect "now". Cheap to call on every key press —
// rendering the strip is O(path-length) and we don't rebuild the prompt
// payload until the user actually sends.
func (m *Model) syncChatContext() {
	snap := sections.BuildContextSnapshot(&m.tree, m.ctx.ActiveContext)
	suggested := []string{}
	// The chat shows starter chips only at the cluster picker (no resource
	// yet) and on unhealthy selections — we centralise that gate in
	// chat_suggestions.go so the chat doesn't need to know k8s health.
	suggested = append(suggested, sections.ChatSuggestionsForRef(m.tree.SelectedRef())...)
	m.chat.SetCrumbs(snap.Crumbs, suggested)
}

// SetContexts seeds the tree's root level with the kube contexts list. The
// app always starts at the picker on row 0 — every launch is an explicit
// "which cluster am I working on right now?" with no defaults, no last-used
// hints, and no muscle-memory shortcuts.
func (m *Model) SetContexts(items []config.ContextInfo) {
	m.tree.SeedContexts(items)
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
	cfg := &config.AppConfig{
		LastCluster:   m.ctx.ActiveContext,
		LastNamespace: m.ctx.ActiveNamespace,
	}
	_ = config.Save(cfg)
}

// Init satisfies tea.Model. Kicks off the auto-refresh tick, the wall-clock
// tick, and the spinner. The app always starts at the context picker — the
// user explicitly chooses which cluster to enter on every launch.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.autoRefreshTick(),
		m.clockTick(),
		m.ctx.Spinner.Tick,
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

// windowTitleFor returns the OS-level terminal window title for the current
// tree path. Operators with several terminal tabs read the title bar to
// identify which cluster (and which resource within it) a tab is pointed at.
// Cluster, then drilled segments — capped so window-list flyouts stay legible.
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
	t := "safina · " + strings.Join(parts, " · ")
	if len(t) > max {
		t = t[:max-1] + "…"
	}
	return t
}

// clockTick emits one clockMsg per second.
func (m *Model) clockTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return clockMsg(t) })
}

// Banner TTLs. Errors stay up longer because operators usually want to read
// the message; success confirmations are deliberately brief — they're just a
// breadcrumb that the action took, not something to study.
const (
	bannerErrorTTL  = 6 * time.Second
	bannerStatusTTL = 3500 * time.Millisecond
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

// clearBanner removes the banner immediately and bumps the epoch so any
// in-flight timer no-ops. Used when a follow-up action supersedes the prior
// banner without raising a new one (e.g., a successful retry after an error).
func (m *Model) clearBanner() {
	m.bannerEpoch++
	m.bannerMsg = ""
	m.bannerErr = false
}
