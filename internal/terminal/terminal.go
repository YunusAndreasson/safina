// Package terminal adapts libghostty's terminal state to Bubble Tea. All methods
// are called on the UI goroutine; native handles never cross session boundaries.
package terminal

import (
	"bytes"
	"errors"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	g "go.mitchellh.com/libghostty"
)

type Terminal struct {
	native        *g.Terminal
	formatter     *g.Formatter
	encoder       *g.KeyEncoder
	render        *g.RenderState
	replies       bytes.Buffer
	width, height uint16
	dirty         bool
	frame         string
	cursor        *tea.Cursor
}

func New(width, height int) (*Terminal, error) {
	t := &Terminal{width: clampSize(width), height: clampSize(height), dirty: true}
	var err error
	t.native, err = g.NewTerminal(g.WithSize(t.width, t.height), g.WithMaxScrollbackBytes(4<<20), g.WithMaxScrollbackLines(1000), g.WithWritePty(func(_ *g.Terminal, data []byte) { t.replies.Write(data) }))
	if err != nil {
		return nil, err
	}
	t.formatter, err = g.NewFormatter(t.native, g.WithFormatterFormat(g.FormatterFormatVT), g.WithFormatterExtras(g.FormatterExtrasNone), g.WithFormatterUnwrap(false), g.WithFormatterTrim(false))
	if err == nil {
		t.encoder, err = g.NewKeyEncoder()
	}
	if err == nil {
		t.render, err = g.NewRenderState()
	}
	if err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func clampSize(n int) uint16 {
	n = min(max(n, 1), 65535)
	return uint16(n)
}

func (t *Terminal) Close() {
	if t == nil {
		return
	}
	if t.render != nil {
		t.render.Close()
		t.render = nil
	}
	if t.encoder != nil {
		t.encoder.Close()
		t.encoder = nil
	}
	if t.formatter != nil {
		t.formatter.Close()
		t.formatter = nil
	}
	if t.native != nil {
		t.native.Close()
		t.native = nil
	}
	t.replies.Reset()
	t.frame = ""
	t.cursor = nil
}

// Feed consumes a remote output chunk and returns any terminal protocol replies.
func (t *Terminal) Feed(data []byte) ([]byte, error) {
	if t.native == nil {
		return nil, io.ErrClosedPipe
	}
	t.native.VTWrite(data)
	t.dirty = true
	return t.takeReplies(), nil
}

func (t *Terminal) takeReplies() []byte {
	out := bytes.Clone(t.replies.Bytes())
	t.replies.Reset()
	return out
}

func (t *Terminal) Resize(width, height int) ([]byte, error) {
	if t.native == nil {
		return nil, io.ErrClosedPipe
	}
	cols, rows := clampSize(width), clampSize(height)
	if err := t.native.Resize(cols, rows, 0, 0); err != nil {
		return nil, err
	}
	t.width, t.height = cols, rows
	t.dirty = true
	return t.takeReplies(), nil
}

func (t *Terminal) Paste(text string) ([]byte, error) {
	if t.native == nil {
		return nil, io.ErrClosedPipe
	}
	// Paste is an explicit user action, retaining Safina's multiline-paste behavior.
	// Ghostty applies the remote program's bracketed-paste and control-byte rules.
	_, err := t.native.Paste(g.Paste{Source: g.PasteSourceText, MIMEs: []string{"text/plain"}, AllowUnsafe: true, Reader: func(_ string, w io.Writer) error { _, err := io.WriteString(w, text); return err }})
	return t.takeReplies(), err
}

func (t *Terminal) Key(key tea.KeyPressMsg) ([]byte, error) {
	if t.native == nil {
		return nil, io.ErrClosedPipe
	}
	// Safina reserves Escape for detaching; Ctrl+[ is its documented way to
	// send Escape to applications, independent of Ghostty's fixterms defaults.
	if key.Code == '[' && key.Mod == tea.ModCtrl {
		key = tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	event, err := g.NewKeyEvent()
	if err != nil {
		return nil, err
	}
	defer event.Close()
	logical := logicalKey(key.Code)
	text := key.Text
	if text == "" && key.Code >= 32 && key.Code < 127 {
		text = string(key.Code)
	}
	var mods g.Mods
	if key.Mod.Contains(tea.ModCtrl) {
		mods |= g.ModCtrl
	}
	if key.Mod.Contains(tea.ModAlt) {
		mods |= g.ModAlt
	}
	if key.Mod.Contains(tea.ModShift) {
		mods |= g.ModShift
	}
	if key.Mod.Contains(tea.ModMeta) || key.Mod.Contains(tea.ModSuper) {
		mods |= g.ModSuper
	}
	// SetFromTerminal turns option-as-alt off, and on macOS that sent Alt+x
	// as a bare "x". The host terminal has already decided what Option
	// means: an Alt here is one it reported, and Option text arrives as text.
	err = errors.Join(event.SetAction(g.KeyActionPress), event.SetKey(logical), event.SetMods(mods), event.SetUTF8(text),
		t.encoder.SetFromTerminal(t.native), t.encoder.SetOptionAsAlt(g.OptionAsAltTrue))
	if err != nil {
		return nil, err
	}
	return t.encoder.Encode(event)
}

// Frame returns only visible cells and styles. Palette, clipboard and terminal
// mode sequences are never forwarded to the host terminal by the formatter.
func (t *Terminal) Frame() (string, *tea.Cursor, error) {
	if t.native == nil {
		return "", nil, io.ErrClosedPipe
	}
	if t.dirty {
		start, err := t.native.GridRef(g.Point{Tag: g.PointTagActive})
		if err != nil {
			return "", nil, err
		}
		end, err := t.native.GridRef(g.Point{Tag: g.PointTagActive, X: t.width - 1, Y: uint32(t.height - 1)})
		if err != nil {
			return "", nil, err
		}
		if err = t.formatter.SetSelection(&g.Selection{Start: *start, End: *end}); err != nil {
			return "", nil, err
		}
		text, err := t.formatter.FormatString()
		if err != nil {
			return "", nil, err
		}
		// The formatter uses CRLF. Lip Gloss composes rows using LF.
		t.frame = lipgloss.NewStyle().Width(int(t.width)).Height(int(t.height)).Render(strings.ReplaceAll(text, "\r\n", "\n"))
		if err = t.render.Update(t.native); err != nil {
			return "", nil, err
		}
		cursor, err := t.render.Cursor()
		if err != nil {
			return "", nil, err
		}
		t.cursor = nil
		if cursor.Visible && cursor.ViewportHasValue {
			shape := tea.CursorBlock
			switch cursor.VisualStyle {
			case g.CursorStyleBar:
				shape = tea.CursorBar
			case g.CursorStyleUnderline:
				shape = tea.CursorUnderline
			case g.CursorStyleBlock, g.CursorStyleBlockHollow: // Host has no hollow-block cursor.
			}
			t.cursor = &tea.Cursor{Position: tea.Position{X: int(cursor.ViewportX), Y: int(cursor.ViewportY)}, Shape: shape, Blink: cursor.Blinking}
		}
		t.dirty = false
	}
	var cursor *tea.Cursor
	if t.cursor != nil {
		position := *t.cursor
		cursor = &position
	}
	return t.frame, cursor, nil
}
