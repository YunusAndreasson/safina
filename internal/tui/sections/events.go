package sections

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Events is the events modal — a snapshot of corev1.Events for a single
// involvedObject (a Deployment or a Pod). For an SRE chasing a regression
// the question is usually "what just happened to this thing?", and this is
// the panel that answers it: newest first, Warnings in red, smart-case
// substring filter, refresh on R.
type Events struct {
	kind      string
	name      string
	namespace string

	items    []k8s.EventInfo
	filtered []int

	vp     viewport.Model
	filter components.Filter
	// Compact rows are the default; w reveals full wrapped messages.
	wrap bool

	loading bool
	err     error

	theme *theme.Theme

	modalW int
	modalH int
}

// NewEvents constructs the events modal. ctx is captured for theme + spinner
// access during async loads.
func NewEvents(c *tuictx.Ctx) Events {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.SoftWrap = false
	return Events{
		vp:     vp,
		wrap:   false,
		filter: components.NewFilter(c.Theme),
		theme:  c.Theme,
	}
}

// Reset prepares the modal for a new resource. The caller follows up with a
// LoadEvents command and SetItems on the result.
func (s *Events) Reset(kind, name, namespace string) {
	s.kind = kind
	s.name = name
	s.namespace = namespace
	s.items = nil
	s.filtered = s.filtered[:0]
	s.filter.Clear()
	s.filter.Close()
	s.loading = true
	s.err = nil
	s.vp.SetContent("")
	s.vp.GotoTop()
}

// FilterActive lets the root tell whether `y` should yank the visible events
// or fall through to the filter input as a literal character.
func (s *Events) FilterActive() bool { return s.filter.Active() }

// YankPayload returns the currently visible (filtered) events as plain text,
// one event per line: "<type>\t<reason>\t<message>". Newest first matches the
// modal's display order so a paste reads top-down the same way the panel does.
func (s *Events) YankPayload() string {
	if len(s.filtered) == 0 {
		return ""
	}
	out := make([]string, 0, len(s.filtered))
	for _, idx := range s.filtered {
		if idx < 0 || idx >= len(s.items) {
			continue
		}
		e := s.items[idx]
		out = append(out, e.Type+"\t"+e.Reason+"\t"+e.Message)
	}
	return strings.Join(out, "\n")
}

// SetItems hands the loaded events (or error) to the modal.
func (s *Events) SetItems(items []k8s.EventInfo, err error) {
	s.items = items
	s.loading = false
	s.err = err
	s.recomputeFilter()
	s.refreshContent()
}

func (s *Events) Title() string {
	return fmt.Sprintf("events · %s · %s", strings.ToLower(s.kind), s.name)
}

func (s *Events) SetSize(termW, termH int) {
	s.modalW, s.modalH = components.ModalFitSize(termW, termH)
	s.recomputeViewportSize()
}

// SetPaneSize sizes the section for inline rendering inside the workspace
// body area (innerW × innerH). The workspace pane owns the outer border;
// modalW/H are back-computed so existing chrome math (ModalChrome = 4)
// resolves the viewport to (innerW, innerH-2). See logs.SetPaneSize.
func (s *Events) SetPaneSize(innerW, innerH int) {
	if innerW < 14 {
		innerW = 14
	}
	if innerH < 4 {
		innerH = 4
	}
	s.modalW = innerW + 2
	s.modalH = innerH + 2
	s.recomputeViewportSize()
}

// Body returns unframed content for the workspace tab embedding.
func (s *Events) Body(c *tuictx.Ctx) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		s.renderStatusRow(c),
		s.viewportBlock(c),
		s.renderKeysRow(),
	)
}

// PaneTitle returns the workspace tab title.
func (s *Events) PaneTitle() string { return s.name }

func (s *Events) recomputeViewportSize() {
	inner := components.PanelContentWidth(s.modalW)
	s.vp.SetWidth(inner)
	h := s.modalH - components.ModalChrome
	if h < 3 {
		h = 3
	}
	s.vp.SetHeight(h)
	s.filter.SetWidth(components.ModalFilterWidth(s.modalW))
	// Re-render rows since column widths changed.
	s.refreshContent()
}

func (s *Events) recomputeFilter() {
	s.filtered = s.filtered[:0]
	q := s.filter.Value()
	if q == "" {
		for i := range s.items {
			s.filtered = append(s.filtered, i)
		}
		return
	}
	cs := hasUpper(q)
	needle := q
	if !cs {
		needle = strings.ToLower(q)
	}
	for i, e := range s.items {
		hay := e.Type + " " + e.Reason + " " + e.Message
		if !cs {
			hay = strings.ToLower(hay)
		}
		if strings.Contains(hay, needle) {
			s.filtered = append(s.filtered, i)
		}
	}
}

func (s *Events) refreshContent() {
	if s.theme == nil {
		return
	}
	inner := components.PanelContentWidth(s.modalW)
	countW := 5
	for _, idx := range s.filtered {
		if s.items[idx].Count > 1 {
			countW = max(countW, ansi.StringWidth(fmt.Sprintf(" ×%d", s.items[idx].Count)))
		}
	}
	var b strings.Builder
	for n, idx := range s.filtered {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s.renderRowWithCountWidth(s.items[idx], inner, countW))
	}
	s.vp.SetContent(b.String())
}

func (s *Events) renderRow(e k8s.EventInfo, w int) string {
	return s.renderRowWithCountWidth(e, w, max(5, ansi.StringWidth(fmt.Sprintf(" ×%d", e.Count))))
}

func (s *Events) renderRowWithCountWidth(e k8s.EventInfo, w, countW int) string {
	t := s.theme
	typeStyle := t.Meta
	if e.Type == "Warning" {
		typeStyle = lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
	}
	// HumanDuration uses at most five cells for compact Kubernetes ages.
	age := lipgloss.NewStyle().Width(9).Align(lipgloss.Right).Render(e.Age + " ago")
	meta := t.Meta.Render(age) + "  " + typeStyle.Render(lipgloss.NewStyle().Width(7).Render(e.Type)) + "  " + t.NameStrong.Render(e.Reason)
	if e.Count > 1 {
		meta += t.Meta.Render(fmt.Sprintf("  ×%d", e.Count))
	}
	message := strings.Join(strings.Fields(e.Message), " ")
	if !s.wrap {
		kind := lipgloss.NewStyle().Width(7).Render(e.Type)
		reasonW := min(24, max(10, w/4))
		reason := lipgloss.NewStyle().Width(reasonW).Render(ansi.Truncate(e.Reason, reasonW, "…"))
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" ×%d", e.Count)
		}
		prefix := t.Meta.Render(age) + " " + typeStyle.Render(kind) + " " + t.NameStrong.Render(reason) + t.Meta.Render(lipgloss.NewStyle().Width(countW).Render(count)) + "  "
		return ansi.Truncate(prefix+message, w, "…")
	}
	return ansi.Wrap(meta, w, "") + "\n" + indent(ansi.Wrap(message, max(1, w-2), ""), "  ")
}

// Update returns (cmd, leave). leave=true closes the modal.
func (s *Events) Update(msg tea.Msg, c *tuictx.Ctx) (tea.Cmd, bool) {
	if paste, ok := msg.(tea.PasteMsg); ok && s.filter.Active() {
		cmd := s.filter.Update(paste)
		s.recomputeFilter()
		s.refreshContent()
		return cmd, false
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd, false
	}

	if s.filter.Active() {
		return s.handleFilterKey(km)
	}

	switch {
	case key.Matches(km, c.Keys.Back), km.String() == "q":
		return nil, true

	case km.String() == "/":
		s.filter.Open()
		s.recomputeViewportSize()
		return nil, false

	case key.Matches(km, c.Keys.Refresh):
		s.loading = true
		return LoadEvents(c.Background, c.Manager, c.ActiveContext, s.namespace, s.kind, s.name), false

	case km.String() == "ctrl+l":
		s.filter.Clear()
		s.recomputeFilter()
		s.refreshContent()
		return nil, false

	case key.Matches(km, c.Keys.Wrap):
		s.wrap = !s.wrap
		s.vp.SoftWrap = s.wrap
		s.refreshContent()
		return nil, false
	}

	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(km)
	return cmd, false
}

func (s *Events) handleFilterKey(km tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, action := s.filter.HandleKey(km)
	switch action {
	case components.FilterCancel:
		s.recomputeFilter()
		s.refreshContent()
		s.recomputeViewportSize()
	case components.FilterCommit:
		s.recomputeViewportSize()
	case components.FilterEdit:
		s.recomputeFilter()
		s.refreshContent()
	}
	return cmd, false
}

func (s *Events) viewportBlock(c *tuictx.Ctx) string {
	st := components.BlockState{}
	switch {
	case s.loading:
		st.Loading = true
		st.Loading_ = "loading events"
	case s.err != nil:
		st.Err = s.err
	case len(s.items) == 0:
		st.Empty = true
		st.Empty_ = "no events"
		st.EmptyArt = components.BeaconArt
		st.EmptyHint = components.RenderModalKeysRow(c.Theme, []components.KeyDesc{{Key: "r", Desc: "refresh"}})
	case len(s.filtered) == 0 && s.filter.Value() != "":
		st.NoMatch = true
	default:
		st.Loaded = true
	}
	return components.CenteredViewportBlock(c.Theme, c.Spinner.View(), s.vp, st)
}

func (s *Events) renderStatusRow(c *tuictx.Ctx) string {
	indicators := make([]components.Indicator, 0, 1)
	if s.filter.Value() != "" {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d/%d matches", len(s.filtered), len(s.items)),
		})
	} else {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d events", len(s.items)),
		})
	}
	return components.RenderModalStatusBar(c.Theme, s.modalW, s.filter, indicators)
}

func (s *Events) renderKeysRow() string {
	wrapDesc := "full messages"
	if s.wrap {
		wrapDesc = "compact rows"
	}
	return components.RenderModalKeysRow(s.theme, []components.KeyDesc{
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
		{Key: "w", Desc: wrapDesc},
		{Key: "j/k", Desc: "scroll"},
		{Key: "ctrl+l", Desc: "clear"},
	})
}
