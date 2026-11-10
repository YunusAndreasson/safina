package terminal_test

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/hinshun/vt10x"
)

// This isolated module compares implementations without changing Safina's
// runtime dependencies. Run with -tags ghostty to include the native backend.
type emulator interface {
	Write([]byte)
	Plain() string
	Resize(int, int)
	Close()
	Key(string) string
	Paste(string) string
}

type factory struct {
	name string
	new  func(testing.TB, int, int) emulator
}

var factories = []factory{{"vt10x", newLegacy}, {"charm", newCharm}}

func normalized(s string) string {
	lines := strings.Split(strings.ReplaceAll(ansi.Strip(s), "\r", ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

type legacy struct{ vt10x.Terminal }

func newLegacy(_ testing.TB, w, h int) emulator { return &legacy{vt10x.New(vt10x.WithSize(w, h))} }
func (e *legacy) Write(b []byte)                { _, _ = e.Terminal.Write(b) }
func (e *legacy) Plain() string                 { return normalized(e.String()) }
func (e *legacy) Close()                        {}

// The legacy key/paste behavior mirrors Safina's current shell adapter, not
// capabilities of vt10x itself (which does not supply a key encoder).
func (e *legacy) Key(key string) string {
	switch key {
	case "up":
		return "\x1b[A"
	case "ctrl-up":
		return "\x1b[A"
	}
	return ""
}
func (e *legacy) Paste(text string) string { return text }

type charm struct {
	*vt.Emulator
	out  bytes.Buffer
	done chan struct{}
}

func newCharm(_ testing.TB, w, h int) emulator {
	e := &charm{Emulator: vt.NewEmulator(w, h), done: make(chan struct{})}
	go func() { _, _ = io.Copy(&e.out, e.Emulator); close(e.done) }()
	return e
}
func (e *charm) Write(b []byte) { _, _ = e.Emulator.Write(b) }
func (e *charm) Plain() string  { return normalized(e.String()) }
func (e *charm) Close()         { _ = e.Emulator.Close(); <-e.done }

// Drain protocol replies throughout the session: output can ask for device
// attributes before the user sends a key. Close/join makes collected input stable.
func (e *charm) input(send func()) string {
	send()
	e.Close()
	return e.out.String()
}

func (e *charm) Key(key string) string {
	if key == "" {
		return e.input(func() {})
	}
	event := uv.KeyPressEvent{Code: uv.KeyUp}
	if key == "ctrl-up" {
		event.Mod = uv.ModCtrl
	}
	return e.input(func() { e.SendKey(event) })
}
func (e *charm) Paste(text string) string { return e.input(func() { e.Emulator.Paste(text) }) }

func TestScreenContracts(t *testing.T) {
	cases := []struct {
		name, input, want string
		bytewise          bool
	}{
		{"overwrite", "hello\rX", "Xello", false},
		{"erase", "hello\r\x1b[2Kbye", "bye", false},
		{"alternate-screen", "main\x1b[?1049halt\x1b[?1049l", "main", false},
		{"wide", "界A", "界A", false},
		{"combining", "e\u0301X", "e\u0301X", false},
		{"emoji", "👩‍💻X", "👩‍💻X", false},
		{"split-utf8", "界A", "界A", true},
		{"split-grapheme", "e\u0301X", "e\u0301X", true},
	}
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					e := f.new(t, 20, 4)
					defer e.Close()
					if tc.bytewise {
						for _, b := range []byte(tc.input) {
							e.Write([]byte{b})
						}
					} else {
						e.Write([]byte(tc.input))
					}
					if got := e.Plain(); got != tc.want {
						t.Errorf("screen=%q want=%q", got, tc.want)
					}
				})
			}
		})
	}
}

func TestInputContracts(t *testing.T) {
	cases := []struct{ name, mode, key, paste, want string }{
		{name: "normal-up", key: "up", want: "\x1b[A"},
		{name: "application-up", mode: "\x1b[?1h", key: "up", want: "\x1bOA"},
		{name: "ctrl-up", key: "ctrl-up", want: "\x1b[1;5A"},
		{name: "plain-paste", paste: "hello", want: "hello"},
		{name: "bracketed-paste", mode: "\x1b[?2004h", paste: "a\nb", want: "\x1b[200~a\nb\x1b[201~"},
	}
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					e := f.new(t, 20, 4)
					defer e.Close()
					e.Write([]byte(tc.mode))
					var got string
					if tc.key != "" {
						got = e.Key(tc.key)
					} else {
						got = e.Paste(tc.paste)
					}
					if got != tc.want {
						t.Errorf("input=%q want=%q", got, tc.want)
					}
				})
			}
		})
	}
}

func TestResizeReflows(t *testing.T) {
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			e := f.new(t, 10, 4)
			defer e.Close()
			e.Write([]byte("abcdefghijklmnop"))
			e.Resize(20, 4)
			if got := e.Plain(); got != "abcdefghijklmnop" {
				t.Errorf("screen=%q want reflowed single line", got)
			}
		})
	}
}

// Parsing a fixed screen without scrolling: no renderer, remote transport, or
// UI frame generation. This avoids comparing different scrollback policies. Native
// allocations do not appear in Go's B/op and allocs/op counters.
func BenchmarkParse(b *testing.B) {
	for _, f := range factories {
		b.Run(f.name, func(b *testing.B) {
			e := f.new(b, 120, 40)
			defer e.Close()
			data := []byte("\x1b[H" + strings.Repeat("\x1b[32m2026-11-07 INFO\x1b[0m request served status=200 latency=12ms\r\n", 32))
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				e.Write(data)
			}
		})
	}
}

var screenSink string

func BenchmarkPlainSnapshot(b *testing.B) {
	for _, f := range factories {
		b.Run(f.name, func(b *testing.B) {
			e := f.new(b, 120, 40)
			defer e.Close()
			e.Write([]byte(strings.Repeat("\x1b[32mINFO\x1b[0m request served status=200 latency=12ms\r\n", 40)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				screenSink = e.Plain()
			}
		})
	}
}

func TestRecordedApplications(t *testing.T) {
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			for _, app := range []string{"vim", "less"} {
				t.Run(app, func(t *testing.T) {
					data, err := os.ReadFile("testdata/" + app + ".vt")
					if err != nil {
						t.Fatal(err)
					}
					// Feed small chunks to exercise real stream boundaries as well as escapes.
					e := f.new(t, 80, 24)
					defer e.Close()
					for len(data) > 0 {
						n := min(7, len(data))
						e.Write(data[:n])
						data = data[n:]
					}
					screen := e.Plain()
					if !strings.Contains(screen, "Safina terminal fixture") {
						t.Errorf("fixture header missing: %q", screen)
					}
					if !strings.Contains(screen, "Unicode: 界 é") {
						t.Errorf("Unicode line corrupted: %q", screen)
					}
				})
			}
		})
	}
}

func TestDeviceReplies(t *testing.T) {
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			e := f.new(t, 20, 4)
			defer e.Close()
			e.Write([]byte("\x1b[6n"))
			if got := e.Key(""); got != "\x1b[1;1R" {
				t.Errorf("reply=%q want cursor position response", got)
			}
		})
	}
}
