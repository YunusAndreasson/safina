package sections

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/hinshun/vt10x"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// shellPhase is the state machine inside the Shell modal. We bundle every
// possible screen into one section because they share the same panel chrome
// and operate on the same target — flipping a phase is cheaper (and less
// prone to keybinding ambiguity) than juggling multiple modal states at the
// root level.
type shellPhase int

const (
	phaseConnect shellPhase = iota // dialing remotecommand
	phaseLive                      // terminal connected, vt10x rendering
	phaseNoShell                   // exec failed because no shell binary — offer debug
	phaseDebug                     // creating netshoot ephemeral container
	phaseError                     // any other dial-time error
)

// shellModalChrome is the rows the panel reserves: 2 borders + 1 status row +
// 1 keys-hint row. The vt10x grid gets everything else.
const shellModalChrome = 4

// vt10x's attribute bits live in an unexported constants block in state.go;
// we pin our own copy here so the renderer can read Glyph.Mode directly.
// These are stable: the package is API-frozen and the bits are part of the
// Glyph layout that callers depend on.
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	vtAttrGfx       = 1 << 3
	vtAttrItalic    = 1 << 4
	vtAttrBlink     = 1 << 5
)

// Shell is the embedded interactive-terminal modal. It owns a vt10x emulator
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
	term    vt10x.Terminal
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
	s.term = vt10x.New(vt10x.WithSize(cols, rows))
}

// Pod returns the current pod target, used by the root for breadcrumbs.
func (s *Shell) Pod() string { return s.pod }

// Paste writes clipboard content to the remote terminal without treating
// pasted letters as Safina shortcuts.
func (s *Shell) Paste(text string) {
	if s.phase == phaseLive && s.session != nil {
		_, _ = s.session.Stdin.Write([]byte(text))
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
		s.term.Resize(cols, rows)
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
	_, _ = s.term.Write(b)
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

// forwardKey translates a tea.KeyPressMsg into the byte sequence a real terminal
// would emit, then writes it to the session's stdin pipe.
func (s *Shell) forwardKey(km tea.KeyPressMsg) {
	if s.session == nil {
		return
	}
	b := keyToBytes(km)
	if len(b) == 0 {
		return
	}
	_, _ = s.session.Stdin.Write(b)
}

// keyToBytes maps Bubble Tea key events to terminal input bytes. Covers the
// keys the on-caller actually uses: navigation, line editing, control codes
// for shell readline, function keys, and printable runes (with alt-prefix).
func keyToBytes(km tea.KeyPressMsg) []byte {
	// Ctrl+letter: emit the raw control byte a real PTY would send
	// (ctrl+a → 0x01, ctrl+z → 0x1a, plus the punctuation block).
	if km.Mod.Contains(tea.ModCtrl) && len(km.Text) == 0 {
		switch {
		case km.Code >= 'a' && km.Code <= 'z':
			return []byte{byte(km.Code - 'a' + 1)}
		case km.Code == '@':
			return []byte{0x00}
		case km.Code == '[':
			return []byte{0x1b}
		case km.Code == '\\':
			return []byte{0x1c}
		case km.Code == ']':
			return []byte{0x1d}
		case km.Code == '^':
			return []byte{0x1e}
		case km.Code == '_':
			return []byte{0x1f}
		}
	}

	switch km.Code {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		if km.Mod.Contains(tea.ModShift) {
			return []byte("\x1b[Z")
		}
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

	if km.Text != "" {
		if km.Mod.Contains(tea.ModAlt) {
			return append([]byte{0x1b}, []byte(km.Text)...)
		}
		return []byte(km.Text)
	}
	return nil
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

	content := lipgloss.JoinVertical(lipgloss.Left,
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
			c.Spinner.View()+" "+t.Idle.Render("creating debug container · "+k8s.DefaultDebugImage+"…"),
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
			t.GreenTxt.Render("↵")+t.DimTxt.Render(" debug  ")+
				t.DimTxt.Render("esc cancel"),
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
			t.DimTxt.Render("press esc to close"),
		)
	case phaseLive:
		return s.renderTerminal(t)
	}
	return centerBox(w, h, "")
}

// runKey identifies a styled run inside a row — every cell with the same key
// renders under one lipgloss.Style call, instead of one Style+Render per cell.
// vt10x.Color is uint32 so the key is small and map-friendly.
type runKey struct {
	fg, bg vt10x.Color
	mode   int16
	cursor bool
}

// renderTerminal walks the vt10x cell grid and turns it into a styled string
// the panel can show. We hold the emulator's lock for the whole walk so the
// background goroutine can't mutate cells mid-frame.
//
// Cells are coalesced into runs of identical attributes per row. A typical
// shell line is ~120 chars but maybe 5–10 attribute changes; emitting a
// single Render per run instead of per cell is a 10×–30× cut in lipgloss
// allocations and ANSI bytes. Trailing default-attr blanks are trimmed
// because PanelWithAccent right-pads short lines anyway. Style values are
// memoized for the frame so identical runs share a prebuilt Style.
func (s *Shell) renderTerminal(t *theme.Theme) string {
	if s.term == nil {
		return centerBox(s.gridCols, s.gridRows, "")
	}
	s.term.Lock()
	defer s.term.Unlock()

	cols, rows := s.term.Size()
	cur := s.term.Cursor()
	cursorVisible := s.term.CursorVisible()

	cache := make(map[runKey]lipgloss.Style, 32)
	getStyle := func(k runKey) lipgloss.Style {
		if st, ok := cache[k]; ok {
			return st
		}
		style := lipgloss.NewStyle()
		if fg := vtColorToLipgloss(k.fg, t, true); fg != nil {
			style = style.Foreground(fg)
		}
		if bg := vtColorToLipgloss(k.bg, t, false); bg != nil {
			style = style.Background(bg)
		}
		if k.mode&vtAttrBold != 0 {
			style = style.Bold(true)
		}
		if k.mode&vtAttrItalic != 0 {
			style = style.Italic(true)
		}
		if k.mode&vtAttrUnderline != 0 {
			style = style.Underline(true)
		}
		if k.cursor {
			style = style.Reverse(true)
		}
		cache[k] = style
		return style
	}

	var out strings.Builder
	out.Grow(cols * rows * 2)
	var run strings.Builder
	var curKey runKey
	hasRun := false

	flush := func() {
		if !hasRun {
			return
		}
		out.WriteString(getStyle(curKey).Render(run.String()))
		run.Reset()
		hasRun = false
	}

	for y := range rows {
		// Find the rightmost meaningful cell on this row so trailing
		// default-blanks aren't styled and emitted. The panel pads short
		// lines, so this is purely a perf win — and a big one because most
		// rows have a lot of empty tail (think prompt + half a command).
		lastIdx := -1
		for x := cols - 1; x >= 0; x-- {
			cell := s.term.Cell(x, y)
			if cell.Char != 0 && cell.Char != ' ' {
				lastIdx = x
				break
			}
			if cell.FG != vt10x.DefaultFG || cell.BG != vt10x.DefaultBG || cell.Mode != 0 {
				lastIdx = x
				break
			}
		}
		if cursorVisible && cur.Y == y && cur.X > lastIdx {
			lastIdx = cur.X
		}

		for x := 0; x <= lastIdx; x++ {
			cell := s.term.Cell(x, y)
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			k := runKey{
				fg:     cell.FG,
				bg:     cell.BG,
				mode:   cell.Mode,
				cursor: cursorVisible && x == cur.X && y == cur.Y,
			}
			if hasRun && k != curKey {
				flush()
			}
			if !hasRun {
				curKey = k
				hasRun = true
			}
			run.WriteRune(ch)
		}
		flush()
		if y < rows-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// vtColorToLipgloss maps a vt10x Color to a lipgloss color string. isFG flips
// the default-color behaviour: for FG we want the theme's default foreground;
// for BG we want "no override" (empty string), so the panel's background
// shows through and selected text remains visible against the panel bg.
func vtColorToLipgloss(c vt10x.Color, t *theme.Theme, isFG bool) color.Color {
	// ANSI palette and truecolor values use the numeric path below.
	//exhaustive:ignore
	switch c {
	case vt10x.DefaultFG:
		if isFG {
			return t.P.FG
		}
		return nil
	case vt10x.DefaultBG, vt10x.DefaultCursor:
		return nil
	default:
		// named ANSI / 256 / truecolor — handled by the numeric path below.
	}
	if c < 256 {
		// ANSI 0..15 + xterm 256-color: pass numeric index through to lipgloss,
		// which knows the standard palette mapping per the user's colorprofile.
		return lipgloss.Color(strconv.Itoa(int(c)))
	}
	// truecolor RGB packed into the low 24 bits.
	r := byte((c >> 16) & 0xff)
	gg := byte((c >> 8) & 0xff)
	b := byte(c & 0xff)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, gg, b))
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

	pad := inner - lipgloss.Width(left) - lipgloss.Width(right)
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
		hints = []components.KeyDesc{{Key: "↵", Desc: "attach debug"}, {Key: "esc", Desc: "cancel"}}
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
		if lipgloss.Width(row+note) <= s.gridCols {
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
