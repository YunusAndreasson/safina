package sections

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/terminal"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// shellPhase is the state machine inside the Shell modal. We bundle every
// possible screen into one section because they share the same panel chrome
// and operate on the same target — flipping a phase is cheaper (and less
// prone to keybinding ambiguity) than juggling multiple modal states at the
// root level.
type shellPhase int

const (
	phaseConnect shellPhase = iota // dialing remotecommand
	phaseLive                      // terminal connected, libghostty rendering
	phaseNoShell                   // exec failed because no shell binary — offer debug
	phaseDebug                     // creating netshoot ephemeral container
	phaseError                     // any other dial-time error
)

// shellModalChrome is the rows the panel reserves: 2 borders + 1 status row +
// 1 keys-hint row. The terminal grid gets everything else.
const shellModalChrome = 4

// Shell is the embedded interactive-terminal modal. It owns a libghostty emulator
// that consumes the byte stream coming back from a kubectl-exec-equivalent
// remotecommand session. Key events are translated to the byte sequences a
// real terminal would send and forwarded into the session's stdin pipe.
//
// Detach key: esc closes the modal, matching every other view in the app.
// That means esc isn't forwarded to the pod's shell — vim users have to use
// Ctrl+[ for escape. The keys row spells this out. Ctrl+C and Ctrl+D pass
// through as SIGINT and EOF, respectively.
type Shell struct {
	phase shellPhase

	// target identifiers
	pod         string
	namespace   string
	container   string // explicit container (Container row); "" lets kubelet pick
	contextName string

	// session state
	term    *terminal.Terminal
	session *k8s.ShellSession
	err     error
	isDebug bool // true once we've attached to a netshoot debug container

	// debug fall-back state
	debugTarget string // container name to share PID namespace with on debug

	theme  *theme.Theme
	modalW int
	modalH int

	// rendered terminal cell dimensions (cols × rows inside the panel)
	gridCols int
	gridRows int
}

// NewShell constructs the section. ctx is captured for theme.
func NewShell(c *tuictx.Ctx) Shell {
	return Shell{theme: c.Theme}
}

// Reset prepares the section for a new target. Called every time the user
// presses S. contextName is the kubeconfig context — only needed at session
// open time, but stored so the section can re-issue AttachDebug without
// round-tripping through the root. The section opens straight into
// phaseConnect; the root issues OpenShell alongside the modal switch so the
// stream starts dialing the moment the modal appears.
func (s *Shell) Reset(contextName, namespace, pod, container string) {
	s.Close()
	s.phase = phaseConnect
	s.contextName = contextName
	s.namespace = namespace
	s.pod = pod
	s.container = container
	s.debugTarget = container
	s.session = nil
	s.err = nil
	s.isDebug = false
	// Pre-allocate the emulator so the grid is renderable from the very
	// first frame — no spinner card while the WebSocket dials. The status
	// row carries the "connecting…" hint instead. SetSize will resize this
	// if the window changes before Attach. Using gridCols/gridRows that
	// SetSize set at modal-open time is safe because the root calls
	// SetSize before Reset's caller hands control back to bubbletea.
	cols, rows := s.gridCols, s.gridRows
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	s.term, s.err = terminal.New(cols, rows)
	if s.err != nil {
		s.phase = phaseError
	}
}

// Pod returns the current pod target, used by the root for breadcrumbs.
func (s *Shell) Pod() string { return s.pod }

// Paste writes clipboard content to the remote terminal without treating
// pasted letters as Safina shortcuts.
func (s *Shell) Paste(text string) {
	if s.phase == phaseLive && s.session != nil {
		data, err := s.term.Paste(text)
		s.sendInput(data, err)
	}
}

// SetSize is invoked on every WindowSizeMsg. Sizes the modal and, if a
// session is live, propagates the new cell dimensions to the pod's TTY so
// vim/less reflow correctly.
func (s *Shell) SetSize(termW, termH int) {
	w := termW - 4
	if w > 160 {
		w = 160
	}
	if w < 40 {
		w = 40
	}
	h := termH - 2
	if h < 14 {
		h = 14
	}
	s.modalW = w
	s.modalH = h

	inner := components.PanelContentWidth(w)
	cols := inner
	rows := h - shellModalChrome
	if cols < 20 {
		cols = 20
	}
	if rows < 5 {
		rows = 5
	}
	s.gridCols = cols
	s.gridRows = rows

	if s.term != nil {
		data, err := s.term.Resize(cols, rows)
		s.sendInput(data, err)
	}
	if s.session != nil {
		// Non-blocking — the resize chan is buffered.
		select {
		case s.session.Resize <- terminalSize(cols, rows):
		default:
		}
	}
}

// Attach is called by the root once OpenShell / AttachDebug returns a live
// session. The emulator was already created in Reset so the user's been
// looking at a blank grid the whole connect — we just flip phase and push
// the initial resize so the pod's shell sees the right cols × rows.
// isDebug records whether this came from the netshoot fall-back so the
// title can reflect it.
func (s *Shell) Attach(sess *k8s.ShellSession, isDebug bool) {
	if s.term == nil {
		sess.Close()
		return
	}
	s.session = sess
	s.isDebug = s.isDebug || isDebug
	s.phase = phaseLive
	select {
	case sess.Resize <- terminalSize(s.gridCols, s.gridRows):
	default:
	}
}

// IngestBytes feeds a stdout chunk into the emulator.
func (s *Shell) IngestBytes(b []byte) {
	if s.term == nil {
		return
	}
	data, err := s.term.Feed(b)
	s.sendInput(data, err)
}

// HandleExit transitions to phaseNoShell or phaseError when the session ends
// before a clean detach. (A clean detach is initiated locally by Ctrl+D and
// doesn't go through this path.)
func (s *Shell) HandleExit(noShell bool, err error) {
	s.session = nil
	if noShell {
		s.phase = phaseNoShell
		return
	}
	if err != nil {
		s.phase = phaseError
		s.err = err
		return
	}
	// Clean exit on the pod side (user typed `exit`); the root tears the
	// modal down on this branch, so phase here is moot — Reset will pick a
	// fresh phaseConnect next time the user opens a shell.
}

// Update returns (cmd, leave). When leave is true, the root closes the modal,
// resets the background context (cancelling any goroutine), and clears its
// session pointer.
func (s *Shell) Update(msg tea.Msg, c *tuictx.Ctx) (tea.Cmd, bool) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		return s.handleKey(km, c)
	}
	return nil, false
}

func (s *Shell) handleKey(km tea.KeyPressMsg, c *tuictx.Ctx) (tea.Cmd, bool) {
	switch s.phase {
	case phaseConnect, phaseDebug:
		// Allow esc to abort while waiting.
		if km.String() == "esc" {
			return nil, true
		}
		return nil, false

	case phaseNoShell:
		switch km.String() {
		case "esc", "q":
			return nil, true
		case "enter":
			s.phase = phaseDebug
			return AttachDebug(c.Background, c.Manager, s.contextName, s.namespace, s.pod, s.debugTarget, ""), false
		}
		return nil, false

	case phaseError:
		// Any escape-ish key closes; everything else is ignored.
		switch km.String() {
		case "esc", "q", "enter":
			return nil, true
		}
		return nil, false

	case phaseLive:
		// Esc detaches the modal — matches every other modal in the app.
		// Ctrl+C and Ctrl+D pass through to the pod (SIGINT / EOF).
		if km.Code == tea.KeyEscape && km.Mod == 0 {
			return nil, true
		}
		s.forwardKey(km)
		return nil, false
	}
	return nil, false
}

// Close releases native state and stops a live session. Reset, detach and
// application shutdown all call this; it is safe to repeat.
func (s *Shell) Close() {
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	if s.term != nil {
		s.term.Close()
		s.term = nil
	}
}

func (s *Shell) sendInput(data []byte, err error) {
	if err == nil && s.session != nil {
		err = s.session.SendInput(data)
	}
	if err != nil {
		s.err = err
		s.phase = phaseError
		if s.session != nil {
			s.session.Close()
		}
	}
}

func (s *Shell) forwardKey(key tea.KeyPressMsg) {
	if s.session == nil || s.term == nil {
		return
	}
	data, err := s.term.Key(key)
	s.sendInput(data, err)
}

// Cursor projects the remote terminal cursor inside the centered panel. Keeping
// it in Bubble Tea avoids custom cell rendering, including wide-character cases.
func (s *Shell) Cursor(width, height int) *tea.Cursor {
	if s.term == nil || s.phase != phaseLive || width < 50 || height < 14 {
		return nil
	}
	_, cursor, err := s.term.Frame()
	if err != nil || cursor == nil {
		return nil
	}
	cursor.X += max(0, width-s.modalW)/2 + 1
	cursor.Y += max(0, height-s.modalH)/2 + 2
	if cursor.X >= width || cursor.Y >= height {
		return nil
	}
	return cursor
}

// View renders the modal centred over a backdrop, dispatching by phase.
func (s *Shell) View(c *tuictx.Ctx, termW, termH int) string {
	t := c.Theme

	title := "shell · " + s.pod
	if s.namespace != "" {
		title = fmt.Sprintf("shell · %s · %s", s.pod, s.namespace)
	}
	if s.isDebug {
		title += " · debug"
	}

	body := s.bodyForPhase(c)
	statusRow := s.statusRow(c)
	keysRow := s.keysRow(t)

	content := textw.JoinVertical(lipgloss.Left,
		statusRow,
		body,
		keysRow,
	)

	// Pink accent on the noshell/error phases is the same convention the
	// confirm modals use: yellow=warn, pink=needs-input. Cyan otherwise —
	// matches Logs (focused, modal, "you are here").
	border, titleColor := t.P.Cyan, t.P.Cyan
	switch s.phase {
	case phaseNoShell:
		border, titleColor = t.P.Yellow, t.P.Yellow
	case phaseError:
		border, titleColor = t.P.Red, t.P.Red
	case phaseConnect, phaseLive, phaseDebug:
		// phaseConnect/phaseLive/phaseDebug — keep cyan accent.
	}

	panel := components.PanelWithAccent(t, title, s.modalW, s.modalH, content, true, border, titleColor)
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, panel,
		components.ModalBackdrop(t)...)
}

// bodyForPhase returns the centre block (everything between status and keys
// rows) sized to fill exactly s.gridCols × s.gridRows.
func (s *Shell) bodyForPhase(c *tuictx.Ctx) string {
	t := c.Theme
	w := s.gridCols
	h := s.gridRows

	switch s.phase {
	case phaseConnect:
		// Render the empty grid right away so the user lands "in" the
		// shell from frame one — the status row's "connecting…" hint
		// covers the dial. No spinner card, no transition flash.
		return s.renderTerminal(t)
	case phaseDebug:
		return centerBox(w, h,
			c.SpinnerView()+" "+t.Idle.Render("creating debug container · "+k8s.DefaultDebugImage+"…"),
			"",
			t.Hint.Render("first pull can take 10–30s"),
		)
	case phaseNoShell:
		yellow := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true)
		return centerBox(w, h,
			yellow.Render("no shell in this container"),
			"",
			t.DimTxt.Render("Attach a debug container with "+k8s.DefaultDebugImage+"?"),
			t.Hint.Render("(includes curl, dig, tcpdump, jq, vim, …)"),
			"",
			components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "↵", Desc: "attach debug", Write: true}, {Key: "esc", Desc: "cancel"}}),
		)
	case phaseError:
		red := lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
		emsg := "unknown error"
		if s.err != nil {
			emsg = s.err.Error()
		}
		return centerBox(w, h,
			red.Render("connection failed"),
			"",
			t.Meta.Render(emsg),
			"",
			components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "esc", Desc: "close"}}),
		)
	case phaseLive:
		return s.renderTerminal(t)
	}
	return centerBox(w, h, "")
}

func (s *Shell) renderTerminal(t *theme.Theme) string {
	if s.term == nil {
		return centerBox(s.gridCols, s.gridRows, "")
	}
	frame, _, err := s.term.Frame()
	if err != nil {
		return t.DimTxt.Render("terminal: " + err.Error())
	}
	return frame
}

// statusRow shows a one-line summary above the body. In phaseLive it doubles
// as a connection/health pill on the right; everywhere else it's a phase
// label on the left.
func (s *Shell) statusRow(c *tuictx.Ctx) string {
	t := c.Theme
	inner := components.PanelContentWidth(s.modalW)

	left := ""
	right := ""
	switch s.phase {
	case phaseConnect:
		left = t.Hint.Render("connecting…")
	case phaseDebug:
		left = t.Hint.Render("attaching debug…")
	case phaseLive:
		right = lipgloss.NewStyle().Foreground(t.P.Green).Bold(true).Render("● connected")
	case phaseNoShell:
		right = lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render("○ no shell")
	case phaseError:
		right = lipgloss.NewStyle().Foreground(t.P.Red).Bold(true).Render("✗ error")
	}

	pad := inner - textw.Width(left) - textw.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// keysRow lists the keys that apply in the current phase.
func (s *Shell) keysRow(t *theme.Theme) string {
	var hints []components.KeyDesc
	switch s.phase {
	case phaseNoShell:
		hints = []components.KeyDesc{{Key: "↵", Desc: "attach debug", Write: true}, {Key: "esc", Desc: "cancel"}}
	case phaseConnect, phaseDebug:
		hints = []components.KeyDesc{{Key: "esc", Desc: "cancel"}}
	case phaseError:
		hints = []components.KeyDesc{{Key: "esc", Desc: "close"}}
	case phaseLive:
		hints = []components.KeyDesc{{Key: "esc", Desc: "detach"}, {Key: "ctrl+c", Desc: "interrupt"}}
	}
	row := components.RenderBoundedHints(t, hints, s.gridCols, components.KeyDesc{})
	if s.phase == phaseLive && s.isDebug {
		note := t.Hint.Render(" · debug container remains until pod deletion")
		if textw.Width(row+note) <= s.gridCols {
			row += note
		}
	}
	return row
}

// centerBox renders the lines vertically centred inside a w × h box. Used by
// every non-live phase so transient screens stay visually anchored.
func centerBox(w, h int, lines ...string) string {
	box := lipgloss.NewStyle().
		Width(w).
		Height(h).
		Align(lipgloss.Center, lipgloss.Center)
	return box.Render(strings.Join(lines, "\n"))
}

func terminalSize(cols, rows int) remotecommand.TerminalSize {
	cols = min(max(cols, 1), 65535)
	rows = min(max(rows, 1), 65535)
	return remotecommand.TerminalSize{Width: uint16(cols), Height: uint16(rows)}
}
