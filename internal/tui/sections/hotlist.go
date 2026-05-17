package sections

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Hot is the noisy-neighbours modal: the top-N pods cluster-wide sorted
// by current CPU usage. The on-caller hits H from anywhere in the tree
// and gets the same answer they'd otherwise piece together by drilling
// per-namespace and scanning sparklines: "what's actually burning right
// now?". Refresh on R; Esc closes.
//
// Deliberately *not* a permanent tree pseudo-section — the user opens it
// to ask a question, sees the answer, and dismisses. Living it as a modal
// keeps the tree's structural identity intact (drill, breadcrumb, history)
// while giving the on-caller the btop-style hot-list they actually need.
type Hot struct {
	items []k8s.PodInfo

	cursor int

	loading bool
	err     error

	// modalW/modalH cache the last viewport size SetSize was handed so
	// View can render at the same dimensions without the caller having
	// to thread them through twice.
	modalW int
	modalH int
}

// NewHot constructs an empty Hot modal. Caller follows up with Reset()
// + LoadHot(...) when the user opens it.
func NewHot() Hot { return Hot{} }

// SetSize stashes the modal viewport dimensions for the next View().
func (s *Hot) SetSize(w, h int) {
	s.modalW = w
	s.modalH = h
}

// Reset prepares the modal for a fresh open. The caller follows with a
// LoadHot command + SetItems on the result.
func (s *Hot) Reset() {
	s.items = nil
	s.cursor = 0
	s.loading = true
	s.err = nil
}

// SetItems hands the loaded hot list (or error) to the modal.
func (s *Hot) SetItems(items []k8s.PodInfo, err error) {
	s.items = items
	s.loading = false
	s.err = err
	if s.cursor >= len(items) {
		s.cursor = 0
	}
}

// CursorUp/CursorDown move the selection within the list, clamped to the
// valid range. The modal's whole purpose is to surface the loud rows at
// the top, so we don't bother wrapping or paging — twenty rows is small
// enough that a few j/k presses suffice.
func (s *Hot) CursorUp() {
	if s.cursor > 0 {
		s.cursor--
	}
}

func (s *Hot) CursorDown() {
	if s.cursor < len(s.items)-1 {
		s.cursor++
	}
}

// Selected returns the highlighted PodInfo, ok=false when the list is
// empty or loading. Used by the root to support "Enter to jump to this
// pod in the tree" if/when that wiring lands.
func (s *Hot) Selected() (k8s.PodInfo, bool) {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return k8s.PodInfo{}, false
	}
	return s.items[s.cursor], true
}

// Body returns the inline unframed content for the workspace tab.
// The workspace pane already owns the surrounding border + title.
func (s *Hot) Body(c *tuictx.Ctx, w, h int) string {
	th := c.Theme
	hint := th.HelpDesc.Render("R refresh · esc close")
	hintRow := lipgloss.NewStyle().Width(w).Align(lipgloss.Right).Render(hint)
	if s.loading {
		return lipgloss.JoinVertical(lipgloss.Left,
			hintRow,
			th.Idle.Render(c.Spinner.View()+" loading hot pods…"))
	}
	if s.err != nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			hintRow,
			th.ErrLine.Render("✗  "+s.err.Error()))
	}
	if len(s.items) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			hintRow,
			th.Idle.Render("no pod metrics available — is metrics-server running?"))
	}
	body := s.renderTable(th, w, h-1)
	return lipgloss.JoinVertical(lipgloss.Left, hintRow, body)
}

// PaneTitle returns the workspace tab title.
func (s *Hot) PaneTitle() string { return "hot pods · top by CPU" }

func (s *Hot) renderTable(th *theme.Theme, w, rowsAvail int) string {
	tableW := w - components.EdgeTotal()
	const (
		kW   = 1
		rkW  = 3 // rank "01."
		nsW  = 18
		stW  = 16
		cpuW = 6 // numeric only, no sparkline (history not guaranteed)
		memW = 7
		// rsW fits the longest plausible restart count (5-digit) and the
		// "restarts" header (8 chars). Was 4 when the header was "RST" — the
		// header text now sets the floor.
		rsW = 8
	)
	gaps := components.GapTotal(8)
	nameW := tableW - kW - rkW - nsW - stW - cpuW - memW - rsW - gaps
	if nameW < 12 {
		nameW = 12
	}

	cols := []int{kW, rkW, nameW, nsW, stW, cpuW, memW, rsW}
	aligns := []components.Align{
		components.AlignLeft, components.AlignRight, components.AlignLeft,
		components.AlignLeft, components.AlignLeft,
		components.AlignRight, components.AlignRight, components.AlignRight,
	}
	header := components.Header(th,
		[]string{"", "#", "name", "namespace", "status", "cpu", "mem", "restarts"},
		cols, aligns)

	start, end := components.VisibleRange(s.cursor, len(s.items), rowsAvail-1)
	out := []string{header}

	// CPU and Mem coloring: peg the top row at 100% (loudest in this list)
	// and scale every row relative to it. That way the gradient still reads
	// even when the cluster is mostly idle and no pod hits 50% in absolute
	// terms — the noisy neighbour is still the "red" one.
	var maxCPU, maxMem int64 = 1, 1
	for _, p := range s.items {
		if p.CPU > maxCPU {
			maxCPU = p.CPU
		}
		if p.Memory > maxMem {
			maxMem = p.Memory
		}
	}

	for i := start; i < end; i++ {
		p := s.items[i]
		selected := i == s.cursor
		bg := th.RowBgFor(i, selected)

		rank := bg.Foreground(th.P.Dim).Render(fmt.Sprintf("%2d.", i+1))
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nameW), bg)
		nsCell := th.MetaOn(components.TruncPad(p.Namespace, nsW), bg)
		statusCell := th.StatusGlyph(p.Status, stW, bg)
		rstCell := th.RestartCount(int(p.Restarts), bg)

		cpuFrac := float64(p.CPU) / float64(maxCPU)
		memFrac := float64(p.Memory) / float64(maxMem)
		cpuCell := bg.Foreground(th.UtilColor(cpuFrac)).Bold(true).
			Render(components.TruncPad(k8s.FormatCPU(p.CPU), cpuW))
		memCell := bg.Foreground(th.UtilColor(memFrac)).Bold(true).
			Render(components.TruncPad(k8s.FormatMem(p.Memory), memW))

		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindPod, ""), rank, nameCell, nsCell, statusCell, cpuCell, memCell, rstCell},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}
