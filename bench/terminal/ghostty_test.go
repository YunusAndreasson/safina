//go:build ghostty

package terminal_test

import (
	"bytes"
	"io"
	"testing"

	g "go.mitchellh.com/libghostty"
)

func init() { factories = append(factories, factory{"ghostty", newGhostty}) }

type ghostty struct {
	t     *g.Terminal
	f     *g.Formatter
	tb    testing.TB
	w, h  int
	input bytes.Buffer
}

func newGhostty(tb testing.TB, w, h int) emulator {
	e := &ghostty{tb: tb, w: w, h: h}
	var err error
	e.t, err = g.NewTerminal(g.WithSize(uint16(w), uint16(h)), g.WithWritePty(func(_ *g.Terminal, b []byte) { e.input.Write(b) }))
	if err != nil {
		tb.Fatal(err)
	}
	e.f, err = g.NewFormatter(e.t, g.WithFormatterFormat(g.FormatterFormatPlain), g.WithFormatterUnwrap(false), g.WithFormatterTrim(true))
	if err != nil {
		e.t.Close()
		tb.Fatal(err)
	}
	return e
}
func (e *ghostty) Write(b []byte) { e.t.VTWrite(b) }
func (e *ghostty) Close()         { e.f.Close(); e.t.Close() }
func (e *ghostty) Resize(w, h int) {
	if err := e.t.Resize(uint16(w), uint16(h), 0, 0); err != nil {
		e.tb.Fatal(err)
	}
	e.w, e.h = w, h
}

func (e *ghostty) Plain() string {
	start, err := e.t.GridRef(g.Point{Tag: g.PointTagActive})
	if err != nil {
		e.tb.Fatal(err)
	}
	end, err := e.t.GridRef(g.Point{Tag: g.PointTagActive, X: uint16(e.w - 1), Y: uint32(e.h - 1)})
	if err != nil {
		e.tb.Fatal(err)
	}
	selection := &g.Selection{Start: *start, End: *end}
	if err = e.f.SetSelection(selection); err != nil {
		e.tb.Fatal(err)
	}
	text, err := e.f.FormatString()
	if err != nil {
		e.tb.Fatal(err)
	}
	return normalized(text)
}

func (e *ghostty) Key(key string) string {
	if key == "" {
		return e.input.String()
	}
	enc, err := g.NewKeyEncoder()
	if err != nil {
		e.tb.Fatal(err)
	}
	defer enc.Close()
	if err = enc.SetFromTerminal(e.t); err != nil {
		e.tb.Fatal(err)
	}
	event, err := g.NewKeyEvent()
	if err != nil {
		e.tb.Fatal(err)
	}
	defer event.Close()
	if err = event.SetAction(g.KeyActionPress); err != nil {
		e.tb.Fatal(err)
	}
	if err = event.SetKey(g.KeyArrowUp); err != nil {
		e.tb.Fatal(err)
	}
	if key == "ctrl-up" {
		if err = event.SetMods(g.ModCtrl); err != nil {
			e.tb.Fatal(err)
		}
	}
	out, err := enc.Encode(event)
	if err != nil {
		e.tb.Fatal(err)
	}
	return string(out)
}

func (e *ghostty) Paste(text string) string {
	e.input.Reset()
	_, err := e.t.Paste(g.Paste{Source: g.PasteSourceText, MIMEs: []string{"text/plain"}, AllowUnsafe: true, Reader: func(_ string, w io.Writer) error { _, err := io.WriteString(w, text); return err }})
	if err != nil {
		e.tb.Fatal(err)
	}
	return e.input.String()
}
