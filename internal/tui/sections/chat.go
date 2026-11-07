package sections

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"

	"github.com/yunus/safina/internal/claude"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Chat is the right-panel conversational assistant. It is always rendered;
// focus controls whether keystrokes go to the textarea or to the tree.
//
// Layout (top → bottom inside the panel border):
//
//	(viewport — turn history. user turns carry their captured breadcrumb.)
//	try:  why CrashLoop?  ·  show last events  ← chips, only when input empty
//	› textarea
//
// We deliberately don't repeat the topbar breadcrumb inside the chat panel:
// the topbar already answers "where am I", and the panel's job is the
// conversation. The crumbs that *do* matter — what was selected when an
// older message was sent — live with the user-turn header in the scrollback.
type Chat struct {
	Focused bool

	svc *claude.Service
	t   *theme.Theme

	// md renders assistant markdown (fenced code, lists, bold) so the
	// panel actually shows ``` blocks as code and not as raw text. Cached
	// and rebuilt only when the viewport width changes — glamour bakes
	// word-wrap into the renderer config, so a resize forces a rebuild.
	md      *glamour.TermRenderer
	mdWidth int

	vp    viewport.Model
	input textarea.Model

	turns []chatTurn

	// Live token-stream destination. When non-empty the trailing turn in
	// `turns` is the assistant turn currently being filled; KindToken events
	// append directly into its Text.
	streaming bool
	cancelFn  context.CancelFunc // cancels just-this-turn (UI-driven cancel)

	// suggested holds the chips for the current selection. Recomputed on
	// every send / focus-change so the chips reflect "now", not the
	// selection at panel creation time.
	suggested []string
	// crumbs are the live context strip visible at all times. Updated by the
	// root model whenever the tree's selection changes — see SetCrumbs.
	crumbs []ContextCrumb

	// retryHint surfaces the most recent api_retry event for ~3s. We don't
	// pile them up — each new one replaces the previous.
	retryHint string

	// errMsg is sticky until the next Send (or until the user clears it).
	errMsg string

	w, h int

	// panelX/panelY are the chat panel's top-left in the outer terminal,
	// stamped by the root each frame via SetScreenOrigin. Combined with
	// inputYInPanel and the textarea's own cursor offset they give the
	// absolute hardware-cursor coordinates Bubble Tea v2 needs.
	panelX, panelY int
	inputYInPanel  int
}

// chatTurn is one rendered exchange. Role is "user" or "assistant" (or
// "system" for in-line install/auth cards). ContextCrumbs is a snapshot of
// what was sent with this turn — so old turns keep their original framing
// even after the user navigates elsewhere.
type chatTurn struct {
	role    string
	text    string
	crumbs  []ContextCrumb
	ts      time.Time
	pending bool // true while streaming into this turn
}

// NewChat constructs the chat section. The Service is shared with the root
// model so cancellation on context-switch composes cleanly with the rest of
// the app's background cancellation.
func NewChat(c *tuictx.Ctx, svc *claude.Service) Chat {
	t := c.Theme

	ta := textarea.New()
	ta.Placeholder = "ask about this resource…"
	ta.ShowLineNumbers = false
	ta.Prompt = "› "
	ta.CharLimit = 4000
	ta.SetHeight(1)
	// Single-line by default; multi-line auto-grows up to the panel cap.
	ta.MaxHeight = 6
	taStyles := ta.Styles()
	taStyles.Focused.Prompt = lipgloss.NewStyle().Foreground(t.P.Pink).Bold(true)
	taStyles.Focused.Text = lipgloss.NewStyle().Foreground(t.P.FG)
	taStyles.Focused.CursorLine = lipgloss.NewStyle()
	taStyles.Blurred.Prompt = lipgloss.NewStyle().Foreground(t.P.Dim)
	taStyles.Blurred.Text = lipgloss.NewStyle().Foreground(t.P.FGFade)
	taStyles.Blurred.Placeholder = t.Hint
	taStyles.Focused.Placeholder = taStyles.Blurred.Placeholder
	taStyles.Cursor.Shape = tea.CursorBar
	taStyles.Cursor.Blink = true
	taStyles.Cursor.Color = t.P.Pink
	ta.SetStyles(taStyles)
	// Switch from the bubbles-drawn block to a real hardware cursor so the
	// terminal's native bar/blink takes over. v2 ties cursor declarative
	// fields on tea.View into the renderer; placement comes from
	// Chat.Cursor() called from the root's View. Without this, every
	// keystroke pumps a tea.Tick for the virtual blink even when the user
	// isn't typing.
	ta.SetVirtualCursor(false)
	// Bubbles binds Enter to insert-newline by default; we want Enter to
	// send. Replacing the binding's keys with something the user is unlikely
	// to type lets us own Enter without the textarea fighting back. Shift+Enter
	// stays available for explicit newlines (handled in Update).
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j")

	vp := viewport.New()
	vp.MouseWheelEnabled = true

	return Chat{
		svc:   svc,
		t:     t,
		input: ta,
		vp:    vp,
	}
}

// SetSize sizes the panel's inner widgets. Called by the root on every
// WindowSizeMsg and on focus changes (because the input height varies).
func (c *Chat) SetSize(w, h int) {
	c.w = w
	c.h = h
	c.layout()
}

// SetCrumbs is called by the root whenever the tree's selection or path
// changes, so the context strip and starter chips stay in sync without the
// user pressing a key.
func (c *Chat) SetCrumbs(crumbs []ContextCrumb, suggested []string) {
	c.crumbs = crumbs
	if !c.streaming {
		c.suggested = suggested
	}
}

// SetFocus toggles focus. When focused, the textarea receives input.
func (c *Chat) SetFocus(focused bool) {
	c.Focused = focused
	if focused {
		c.input.Focus()
	} else {
		c.input.Blur()
	}
	c.layout()
}

// HasInput reports whether the textarea currently holds text. Used by the
// root to decide whether `esc` should clear the draft or unfocus the panel.
func (c *Chat) HasInput() bool { return strings.TrimSpace(c.input.Value()) != "" }

// SetScreenOrigin records where the chat panel's top-left lands in the
// outer terminal so Cursor() can return absolute coordinates. The root
// computes this each frame from the layout split; we stash it because the
// chat itself only knows about its own width/height.
func (c *Chat) SetScreenOrigin(x, y int) {
	c.panelX = x
	c.panelY = y
}

// Cursor returns the hardware cursor placement for the textarea, or nil
// when the chat isn't focused (the cursor should hide while the tree owns
// the keystream). Caller wires the result into tea.View.Cursor.
func (c *Chat) Cursor() *tea.Cursor {
	if !c.Focused {
		return nil
	}
	cur := c.input.Cursor()
	if cur == nil {
		return nil
	}
	cur.X += c.panelX + 1 // +1 for the panel's left border
	cur.Y += c.panelY + c.inputYInPanel
	return cur
}

// YankPayload returns the most recent assistant turn's markdown source —
// what we sent through the model, before glamour rendered it for the
// terminal. The user typically wants the pasteable Markdown (paste into a
// PR description, a runbook, a GitHub comment), not the ANSI-coloured copy.
// Returns "" while the chat panel is empty or the only turn is the user's.
func (c *Chat) YankPayload() string {
	for i := len(c.turns) - 1; i >= 0; i-- {
		if c.turns[i].role == "assistant" {
			return c.turns[i].text
		}
	}
	return ""
}

// Streaming reports whether a turn is currently being filled.
func (c *Chat) Streaming() bool { return c.streaming }

// CancelStream pre-empts an in-flight assistant turn. Wired to esc-while-
// streaming.
func (c *Chat) CancelStream() {
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
	if c.svc != nil {
		c.svc.Cancel()
	}
	if c.streaming && len(c.turns) > 0 {
		last := &c.turns[len(c.turns)-1]
		if last.role == "assistant" && last.pending {
			last.text = strings.TrimRight(last.text, "\n") + "\n[cancelled]"
			last.pending = false
		}
	}
	c.streaming = false
}

// ResetSession starts a new conversation. Wired to Ctrl+N.
func (c *Chat) ResetSession() {
	c.CancelStream()
	c.turns = nil
	c.errMsg = ""
	c.retryHint = ""
	if c.svc != nil {
		c.svc.ResetSession()
	}
}

// ── Update / streaming wire ──────────────────────────────────────────

// SubmitMsg is emitted by the chat when the user submits a prompt; the root
// turns it into a streaming subprocess.
type SubmitMsg struct {
	Prompt        string
	ContextBlock  string
	ContextCrumbs []ContextCrumb
}

// Every event carries its source channel so late events from cancelled
// turns cannot modify a newer conversation.
type ChatTokenMsg struct {
	Ch    <-chan claude.Event
	Delta string
}

type ChatRetryMsg struct {
	Ch      <-chan claude.Event
	Attempt int
	Of      int
	DelayMS int
	Reason  string
}

type ChatTurnDoneMsg struct {
	Ch  <-chan claude.Event
	Err error
}

// NextChatEvent reads one event from the stream channel and translates it to
// the matching tea.Msg. Mirrors the nextLogLine pattern used by the logs
// section so the renderer stays in lock-step with the producer.
func NextChatEvent(ch <-chan claude.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return ChatTurnDoneMsg{Ch: ch}
		}
		switch ev.Kind {
		case claude.KindToken:
			return ChatTokenMsg{Ch: ch, Delta: ev.Delta}
		case claude.KindRetry:
			return ChatRetryMsg{
				Ch:      ch,
				Attempt: ev.Attempt, Of: ev.MaxRetries,
				DelayMS: ev.DelayMS, Reason: ev.Reason,
			}
		case claude.KindDone:
			return ChatTurnDoneMsg{Ch: ch, Err: ev.Err}
		case claude.KindInit:
			// Init events carry the session id but the Service tracks that
			// internally; nothing for the UI to do — recurse and read again.
			return NextChatEvent(ch)()
		}
		return NextChatEvent(ch)()
	}
}

// IngestToken appends a streamed text delta into the active assistant turn.
func (c *Chat) IngestToken(delta string) {
	if !c.streaming || len(c.turns) == 0 {
		return
	}
	last := &c.turns[len(c.turns)-1]
	if last.role != "assistant" {
		return
	}
	last.text += delta
	c.refreshViewport()
	c.vp.GotoBottom()
}

// IngestRetry replaces the current retry banner.
func (c *Chat) IngestRetry(attempt, of, delayMS int, reason string) {
	c.retryHint = fmt.Sprintf("retrying %s (%d/%d, %ds)…", reason, attempt, of, delayMS/1000)
}

// FinishTurn is the terminal step of a turn. err is non-nil on failure; on
// success the assistant turn is committed as-is.
func (c *Chat) FinishTurn(err error) {
	c.streaming = false
	c.cancelFn = nil
	c.retryHint = ""
	if len(c.turns) > 0 {
		c.turns[len(c.turns)-1].pending = false
	}
	if err != nil {
		c.errMsg = err.Error()
		// If nothing streamed, drop the empty assistant placeholder so the
		// error lives in the dedicated banner instead of as a blank turn.
		if len(c.turns) > 0 {
			last := &c.turns[len(c.turns)-1]
			if last.role == "assistant" && strings.TrimSpace(last.text) == "" {
				c.turns = c.turns[:len(c.turns)-1]
			}
		}
	}
	c.refreshViewport()
	c.vp.GotoBottom()
}

// Update handles keystrokes when the chat is focused. Returns:
//   - cmd to run
//   - submit: a SubmitMsg if the user pressed enter on a non-empty draft
//     (the root packages it with a fresh ContextSnapshot before sending)
//   - leave: focus should return to the tree
func (c *Chat) Update(msg tea.Msg, ctx *tuictx.Ctx) (cmd tea.Cmd, submit *SubmitMsg, leave bool) {
	if !c.Focused {
		return nil, nil, false
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		// Non-key messages: forward to the viewport (scroll-related ticks)
		// AND the textarea (PasteMsg in particular — bracketed-paste mode
		// is on by default in v2 so multi-line pastes arrive here as one
		// PasteMsg instead of fragmented keystrokes; the textarea handles
		// it natively, the chat just has to deliver it).
		var vc, ic tea.Cmd
		c.vp, vc = c.vp.Update(msg)
		c.input, ic = c.input.Update(msg)
		c.layout()
		return tea.Batch(vc, ic), nil, false
	}

	switch km.String() {
	case "tab":
		return nil, nil, true

	case "esc":
		// Layered behavior so esc means the most useful thing in context.
		// Doherty: each press should obviously change something.
		switch {
		case c.streaming:
			c.CancelStream()
			c.refreshViewport()
			return nil, nil, false
		case c.HasInput():
			c.input.Reset()
			return nil, nil, false
		default:
			return nil, nil, true
		}

	case "pgup":
		c.vp.PageUp()
		return nil, nil, false

	case "pgdown":
		c.vp.PageDown()
		return nil, nil, false

	case "ctrl+l":
		// Clear scrollback but keep the conversation id — sometimes you
		// want a clean panel without losing context.
		c.CancelStream()
		c.turns = nil
		c.errMsg = ""
		c.retryHint = ""
		c.refreshViewport()
		return nil, nil, false

	case "ctrl+n":
		c.ResetSession()
		c.refreshViewport()
		return nil, nil, false

	case "enter":
		draft := strings.TrimSpace(c.input.Value())
		if draft == "" || c.streaming {
			return nil, nil, false
		}
		c.input.Reset()
		c.errMsg = ""
		// Append the user turn now so the panel feels responsive even
		// before claude's first byte. Crumbs are snapshotted from c.crumbs
		// (the most-recent SetCrumbs from the root).
		c.turns = append(c.turns, chatTurn{
			role:   "user",
			text:   draft,
			crumbs: append([]ContextCrumb(nil), c.crumbs...),
			ts:     time.Now(),
		})
		// Placeholder assistant turn — IngestToken will fill it.
		c.turns = append(c.turns, chatTurn{
			role:    "assistant",
			pending: true,
			ts:      time.Now(),
		})
		c.streaming = true
		c.refreshViewport()
		c.vp.GotoBottom()
		// The actual context block is built by the root (it has the tree
		// reference) and packaged into the SubmitMsg.
		out := &SubmitMsg{Prompt: draft}
		return nil, out, false

	case "shift+enter":
		// Explicit newline.
		c.input.InsertRune('\n')
		c.layout()
		return nil, nil, false
	}

	// Click on a chip: number-key shortcut. 1-3 triggers the matching chip.
	if r := km.String(); len(r) == 1 && r[0] >= '1' && r[0] <= '9' && !c.HasInput() && !c.streaming {
		idx := int(r[0] - '1')
		if idx < len(c.suggested) {
			c.input.SetValue(c.suggested[idx])
			c.input.CursorEnd()
			return nil, nil, false
		}
	}

	var ic tea.Cmd
	c.input, ic = c.input.Update(km)
	c.layout()
	return ic, nil, false
}

// ── Layout / render ──────────────────────────────────────────────────

// layout sizes the textarea and viewport from the current outer (w, h).
// Called whenever w/h, focus, or input height change.
func (c *Chat) layout() {
	if c.w < 4 || c.h < 6 {
		return
	}
	innerW := components.PanelContentWidth(c.w)
	innerH := c.h - 2 // top + bottom border

	// Reserve rows: viewport fills the top, then a 1-row hairline rule,
	// then chips (0 or 1), banner (0 or 1), and the input (1..MaxHeight).
	const ruleRows = 1
	chipRows := 0
	if c.shouldShowChips() {
		chipRows = 1
	}
	bannerRows := 0
	if c.errMsg != "" || c.retryHint != "" {
		bannerRows = 1
	}

	// Auto-grow the textarea up to MaxHeight, but never past half the panel.
	desired := c.input.LineCount()
	if desired < 1 {
		desired = 1
	}
	maxRows := innerH / 2
	if maxRows < 1 {
		maxRows = 1
	}
	if desired > maxRows {
		desired = maxRows
	}
	if desired > c.input.MaxHeight {
		desired = c.input.MaxHeight
	}
	c.input.SetWidth(innerW)
	c.input.SetHeight(desired)

	vpH := innerH - ruleRows - chipRows - bannerRows - desired
	if vpH < 1 {
		vpH = 1
	}
	c.vp.SetWidth(innerW)
	c.vp.SetHeight(vpH)
	c.refreshViewport()

	// Stash the textarea's first-line offset within the panel so the root
	// can place a hardware cursor on top of it from View(). Layout is the
	// only place that knows the live row split.
	c.inputYInPanel = 1 + vpH + ruleRows + chipRows + bannerRows
}

// shouldShowChips returns true when the input is empty AND we have starter
// suggestions for the current selection. Avoids visual noise when the user
// is mid-typing or when the resource doesn't have any hint to offer.
func (c *Chat) shouldShowChips() bool {
	return !c.streaming && !c.HasInput() && len(c.suggested) > 0 && len(c.turns) == 0
}

func (c *Chat) refreshViewport() {
	c.vp.SetContent(c.renderTurns())
}

// renderTurns paints the conversation history into the viewport content
// string. Each turn is two parts: a single-line role header (with its
// captured-at-send-time context strip), then the body indented two cells.
func (c *Chat) renderTurns() string {
	if len(c.turns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(c.turns)*2)
	for i, turn := range c.turns {
		if i > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, c.renderTurnHeader(turn))
		parts = append(parts, c.renderTurnBody(turn))
	}
	return strings.Join(parts, "\n")
}

// renderTurnHeader is the "▸ you" / "◆ ai" label plus, for user turns, the
// breadcrumb captured when the message was sent. Old turns keep their
// original context — the strip at the top of the panel always shows
// "now", but the historical turn shows "what was selected when I asked".
func (c *Chat) renderTurnHeader(turn chatTurn) string {
	p := c.t.P
	switch turn.role {
	case "user":
		head := lipgloss.NewStyle().Foreground(p.Cyan).Bold(true).Render("▸ you")
		if len(turn.crumbs) > 0 {
			strip := renderCrumbStrip(turn.crumbs, p.Dim, p.FGFade, true)
			return ansi.Truncate(head+"  "+lipgloss.NewStyle().Foreground(p.Dim).Render(strip), c.vp.Width(), "…")
		}
		return head
	case "assistant":
		head := lipgloss.NewStyle().Foreground(p.Pink).Bold(true).Render("◆ ai")
		if turn.pending {
			head += "  " + c.t.Hint.Render("…")
		}
		return head
	default:
		return c.t.Hint.Render(turn.text)
	}
}

func (c *Chat) renderTurnBody(turn chatTurn) string {
	if turn.role == "assistant" {
		if turn.pending && turn.text == "" {
			return c.t.Hint.Render("  thinking…")
		}
		body := c.renderMarkdown(turn.text)
		if turn.pending {
			// Streaming cursor — visual proof that more tokens are coming.
			// Cyan to match the panel's focus accent; placed flush with
			// the last character (no leading space) so it looks like a
			// caret rather than a dot floating in the margin.
			body += lipgloss.NewStyle().Foreground(c.t.P.Cyan).Render("▍")
		}
		return body
	}
	return c.plainBody(turn.text)
}

func (c *Chat) plainBody(s string) string {
	return indent(ansi.Wrap(s, max(1, c.vp.Width()-2), ""), "  ")
}

// renderMarkdown turns the assistant's raw markdown into ANSI-styled text
// (fenced code, lists, **bold**). Glamour brings its own left margin so we
// don't add a manual indent — stacking the two would push the body four
// cells in from the panel border, eating already-tight width.
//
// During streaming, the markdown is necessarily incomplete (the closing
// ``` may not have arrived yet). Glamour / goldmark is permissive enough
// to render that gracefully, so we just keep re-rendering on every token —
// the result reflows when the fence closes. If the renderer errors out we
// fall back to plain indented text rather than show nothing.
func (c *Chat) renderMarkdown(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if err := c.ensureMD(); err != nil || c.md == nil {
		return c.plainBody(s)
	}
	out, err := c.md.Render(s)
	if err != nil {
		return c.plainBody(s)
	}
	return strings.TrimRight(out, "\n")
}

// ensureMD lazily builds (or rebuilds, on width change) the glamour
// renderer. We pin the style to "tokyo-night" so the chat's markdown reads
// as part of safina's palette rather than fighting it.
func (c *Chat) ensureMD() error {
	w := c.vp.Width()
	if w < 4 {
		w = 4
	}
	if c.md != nil && c.mdWidth == w {
		return nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("tokyo-night"),
		glamour.WithWordWrap(w),
	)
	if err != nil {
		return err
	}
	c.md = r
	c.mdWidth = w
	return nil
}

// View renders the entire panel — kept for callers that want a self-framed
// chat block. The workspace tab path uses Body() instead so the workspace's
// own panel border isn't doubled.
func (c *Chat) View(ctx *tuictx.Ctx, w, h int) string {
	if c.w != w || c.h != h {
		c.SetSize(w, h)
	}
	t := ctx.Theme
	body := c.renderInner(t)
	return components.PanelWithAccent(t, "Chat", w, h, body, c.Focused, t.P.Cyan, t.P.Cyan)
}

// Body returns the unframed chat content (viewport + chips + input) sized
// for embedding as a workspace tab. The workspace pane owns the outer
// border, so we hand back just the inner stack.
func (c *Chat) Body(ctx *tuictx.Ctx, w, h int) string {
	// SetSize subtracts the panel chrome internally; we lie about size by
	// padding the requested area so the inner widgets size to (w, h).
	if c.w != w+2 || c.h != h+2 {
		c.SetSize(w+2, h+2)
	}
	return c.renderInner(ctx.Theme)
}

// PaneTitle returns the workspace tab title for the chat panel.
func (c *Chat) PaneTitle() string { return "chat" }

func (c *Chat) renderInner(t *theme.Theme) string {
	parts := []string{c.viewportBlock(t)}
	// A thin hairline separates the conversation/empty area from the
	// compose row. Without it, the textarea floats in negative space
	// and the panel feels structureless. Drawn dim so it sits behind
	// content, not in front of it (Aesthetic-Usability: chrome should
	// not compete with data).
	parts = append(parts, c.renderRule(t))
	if c.errMsg != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Red).Render("✗ "+c.errMsg))
	} else if c.retryHint != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Yellow).Italic(true).Render(c.retryHint))
	}
	if c.shouldShowChips() {
		parts = append(parts, c.renderChips(t))
	}
	parts = append(parts, c.input.View())
	return strings.Join(parts, "\n")
}

// renderRule draws the single-line dim hairline that separates the
// conversation area from the input area. Width matches the panel content
// width so it lines up edge-to-edge with the border.
func (c *Chat) renderRule(t *theme.Theme) string {
	w := components.PanelContentWidth(c.w)
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Foreground(t.P.Dim).Render(strings.Repeat("─", w))
}

// renderChips draws the numbered starter suggestions. We label them 1/2/3
// because vim-keyboard users will hit the digit keys faster than they'll
// reach for the mouse — and the digits double as a visual count.
func (c *Chat) renderChips(t *theme.Theme) string {
	if len(c.suggested) == 0 {
		return ""
	}
	hint := lipgloss.NewStyle().Foreground(t.P.Dim).Render("try  ")
	chipStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)
	keyStyle := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)
	sep := lipgloss.NewStyle().Foreground(t.P.Dim).Render("  ·  ")

	chips := make([]string, 0, len(c.suggested))
	for i, s := range c.suggested {
		chips = append(chips, keyStyle.Render(strconv.Itoa(i+1))+chipStyle.Render(" "+s))
	}
	return hint + strings.Join(chips, sep)
}

func (c *Chat) viewportBlock(t *theme.Theme) string {
	if !c.svc.Available() {
		return c.renderInstallCard(t)
	}
	// Empty state: render absolutely nothing in the viewport. The textarea
	// already shows "ask about this resource…" as its placeholder, so a
	// second hint floating in the viewport is just duplication; centering
	// it vertically made the panel look unbalanced (one line marooned in
	// the middle of a tall void). Empty space is a fine state for an
	// empty chat — same as a fresh shell prompt.
	if len(c.turns) == 0 && c.errMsg == "" {
		return c.padToHeight("")
	}
	return c.vp.View()
}

// padToHeight returns content padded with trailing newlines to fill exactly
// c.vp.Height rows, so the panel's row math stays honest regardless of how
// short the viewport content is. lipgloss.Height treats both "" and a
// no-newline string as one line, so subtracting from c.vp.Height and
// appending that many '\n' produces exactly c.vp.Height lines either way.
func (c *Chat) padToHeight(s string) string {
	h := c.vp.Height()
	if h < 1 {
		return s
	}
	have := lipgloss.Height(s)
	if have >= h {
		return s
	}
	return s + strings.Repeat("\n", h-have)
}

// renderInstallCard is the "claude not found" stand-in for the viewport
// area. Anchored top-left (not vertically centered) so the panel reads as
// a notice card rather than a floating message — same pattern as the
// rest of safina's empty / error states.
func (c *Chat) renderInstallCard(t *theme.Theme) string {
	title := t.Divider("install Claude to enable chat", components.PanelContentWidth(c.w))
	step := lipgloss.NewStyle().Foreground(t.P.FG)
	cmd := lipgloss.NewStyle().Foreground(t.P.FGFade)
	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	body := strings.Join([]string{
		title,
		"",
		step.Render(" 1  ") + cmd.Render("npm i -g @anthropic-ai/claude-code"),
		step.Render(" 2  ") + cmd.Render("claude") + dim.Render("  → ") + cmd.Render("/login"),
		step.Render(" 3  ") + cmd.Render("relaunch safina"),
		"",
		t.Hint.Render("uses your Pro/Max subscription — no API key needed."),
	}, "\n")
	return c.padToHeight(body)
}

// ── helpers ──────────────────────────────────────────────────────────

// renderCrumbStrip joins crumbs with the same `›` separator the topbar uses,
// optionally wrapping each label in dim styling. When `compact` is true we
// drop the leading glyph (used in turn headers where space is tight).
func renderCrumbStrip(crumbs []ContextCrumb, sepCol, labelCol color.Color, compact bool) string {
	sep := lipgloss.NewStyle().Foreground(sepCol).Render(" › ")
	label := lipgloss.NewStyle().Foreground(labelCol)
	glyph := lipgloss.NewStyle().Foreground(sepCol)
	parts := make([]string, 0, len(crumbs))
	for _, cr := range crumbs {
		seg := label.Render(cr.Label)
		if !compact && cr.Glyph != "" {
			seg = glyph.Render(cr.Glyph) + " " + seg
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, sep)
}

func indent(s, prefix string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}
