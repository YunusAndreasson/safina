package tuictx

import (
	"context"

	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
)

// Ctx is the program context threaded through every section.
//
// It groups everything a section can read but should not mutate directly:
// theme, keymap, terminal dimensions, the kubernetes manager, app config,
// and the currently active context/namespace. State changes (e.g. switching
// context) flow through messages handled by the root, which then updates Ctx.
type Ctx struct {
	Theme *theme.Theme
	Keys  keys.Map

	Width  int
	Height int

	Manager *k8s.Manager
	AppCfg  *config.AppConfig

	ActiveContext   string
	ActiveNamespace string

	// Background carries cancellation for in-flight k8s requests. It is reset
	// when the user leaves a long-running view (e.g. log streaming).
	Background context.Context
	Cancel     context.CancelFunc

	// Spinner is the shared loading indicator. Sections render Spinner.View()
	// in their loading branches; the root forwards spinner.TickMsg to keep it
	// animated. One spinner for the whole app keeps every loader on the same
	// frame.
	Spinner spinner.Model
}

// New builds a Ctx with a fresh background context.
func New(t *theme.Theme, k keys.Map, mgr *k8s.Manager, cfg *config.AppConfig) *Ctx {
	bg, cancel := context.WithCancel(context.Background())
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(t.P.Cyan)
	return &Ctx{
		Theme:      t,
		Keys:       k,
		Manager:    mgr,
		AppCfg:     cfg,
		Background: bg,
		Cancel:     cancel,
		Spinner:    sp,
	}
}

// ResetBackground replaces the cancellation context. Call when leaving a view
// that owns long-running goroutines (e.g. log streams).
func (c *Ctx) ResetBackground() {
	if c.Cancel != nil {
		c.Cancel()
	}
	c.Background, c.Cancel = context.WithCancel(context.Background())
}
