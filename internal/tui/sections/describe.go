package sections

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Describe is the YAML-dump modal — kubectl get -o yaml in a navigable view.
// Smart-case filter narrows AND highlights matches inline so an SRE can
// scan a 300-line manifest for "image:" or "annotations" without grepping.
type Describe struct {
	kind      string
	name      string
	namespace string

	yaml        string
	lines       []string
	highlighted []string
	filtered    []int

	vp     viewport.Model
	filter components.Filter
	// wrap drives the viewport's soft-wrap mode. Default off — YAML
	// indentation conveys structure best when long values truncate at
	// the panel edge; `w` toggles wrap for the times a long inline
	// string (image refs, annotations) needs the full read.
	wrap bool

	loading  bool
	err      error
	revealed bool // true when this is a Secret reveal — adds a "decoded" cue

	theme *theme.Theme

	sourceGVR schema.GroupVersionResource
	modalW    int
	modalH    int
}

// NewDescribe constructs the describe modal.
func NewDescribe(c *tuictx.Ctx) Describe {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	return Describe{
		vp:     vp,
		filter: components.NewFilter(c.Theme),
		theme:  c.Theme,
	}
}

// Reset prepares for a new resource. Caller follows with LoadDescribe + SetYAML.
func (s *Describe) Reset(kind, name, namespace string) {
	s.kind = kind
	s.sourceGVR = schema.GroupVersionResource{}
	s.name = name
	s.namespace = namespace
	s.yaml = ""
	s.lines = nil
	s.highlighted = nil
	s.filtered = s.filtered[:0]
	s.filter.Clear()
	s.filter.Close()
	s.loading = true
	s.err = nil
	s.revealed = false
	s.vp.SetContent("")
	s.vp.GotoTop()
}

// SetSourceGVR keeps refreshes on the API used to open a generic resource.
func (s *Describe) SetSourceGVR(gvr schema.GroupVersionResource) { s.sourceGVR = gvr }

// MarkRevealed flips the modal into "decoded" mode — the title leads with
// "decoded" and a yellow banner above the YAML body warns that values are
// shown in plaintext. Call this after Reset when opening a Secret reveal.
func (s *Describe) MarkRevealed() {
	s.revealed = true
	s.recomputeViewportSize()
}

// FilterActive lets the root tell whether `y` should yank the YAML or fall
// through to the filter input as a literal character.
func (s *Describe) FilterActive() bool { return s.filter.Active() }

// YankPayload returns the currently visible (filtered) lines of the manifest
// joined with newlines. With no filter that's the full YAML; with a filter
// it's just the matching lines, on the same "the filter is the operator's
// scope" reasoning as logs and events.
func (s *Describe) YankPayload() string {
	if len(s.filtered) == 0 {
		return s.yaml
	}
	out := make([]string, 0, len(s.filtered))
	for _, idx := range s.filtered {
		if idx >= 0 && idx < len(s.lines) {
			out = append(out, s.lines[idx])
		}
	}
	return strings.Join(out, "\n")
}

// SetYAML hands the loaded manifest (or error) to the modal.
func (s *Describe) SetYAML(yaml string, err error) {
	s.yaml = yaml
	s.lines = strings.Split(strings.TrimRight(yaml, "\n"), "\n")
	s.highlighted = strings.Split(components.HighlightYAML(strings.Join(s.lines, "\n"), s.theme), "\n")
	s.loading = false
	s.err = err
	s.recomputeFilter()
	s.refreshContent()
}

func (s *Describe) Title() string {
	verb := "YAML"
	if s.revealed {
		verb = "decoded"
	}
	return fmt.Sprintf("%s · %s · %s", verb, strings.ToLower(s.kind), s.name)
}

func (s *Describe) SetSize(termW, termH int) {
	s.modalW, s.modalH = components.ModalFitSize(termW, termH)
	s.recomputeViewportSize()
}

// SetPaneSize sizes the section for inline rendering inside the workspace
// body area (innerW × innerH). modalW/H are back-computed so ModalChrome
// math resolves the viewport to (innerW, innerH-2). See logs.SetPaneSize.
func (s *Describe) SetPaneSize(innerW, innerH int) {
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

// Body returns unframed content for workspace tab embedding. When the
// describe is a Secret reveal, a yellow banner prefixes the body to make
// "this is decoded plaintext, not base64" loud — same cue the legacy
// modal carried.
func (s *Describe) Body(c *tuictx.Ctx) string {
	body := s.viewportBlock(c)
	if s.revealed {
		banner := lipgloss.NewStyle().
			Foreground(c.Theme.P.Yellow).
			Bold(true).
			Render("⚠ decoded — values are plaintext")
		body = textw.JoinVertical(lipgloss.Left, banner, body)
	}
	if row, ok := components.FilterRow(c.Theme, s.filter); ok {
		body = textw.JoinVertical(lipgloss.Left, row, body)
	}
	return body
}

// PaneTitle returns the workspace tab title.
func (s *Describe) PaneTitle() string { return s.name }

func (s *Describe) recomputeViewportSize() {
	inner := components.PanelContentWidth(s.modalW)
	s.vp.SetWidth(inner)
	chrome := 2
	if s.filter.Active() || s.filter.Value() != "" {
		chrome++
	}
	if s.revealed {
		// One extra row for the "decoded" banner above the body.
		chrome++
	}
	h := s.modalH - chrome
	if h < 3 {
		h = 3
	}
	s.vp.SetHeight(h)
	s.filter.SetWidth(components.ModalFilterWidth(s.modalW))
}

func (s *Describe) recomputeFilter() {
	s.filtered = s.filtered[:0]
	q := s.filter.Value()
	if q == "" {
		for i := range s.lines {
			s.filtered = append(s.filtered, i)
		}
		return
	}
	cs := hasUpper(q)
	needle := q
	if !cs {
		needle = strings.ToLower(q)
	}
	for i, ln := range s.lines {
		hay := ln
		if !cs {
			hay = strings.ToLower(ln)
		}
		if strings.Contains(hay, needle) {
			s.filtered = append(s.filtered, i)
		}
	}
}

func (s *Describe) refreshContent() {
	if s.theme == nil {
		return
	}
	var b strings.Builder
	for n, idx := range s.filtered {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s.highlighted[idx])
	}
	s.vp.SetContent(b.String())
}

func (s *Describe) Update(msg tea.Msg, c *tuictx.Ctx) (tea.Cmd, bool) {
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
		if !s.sourceGVR.Empty() {
			return LoadGenericDescribe(c.Background, c.Manager, c.ActiveContext, NodeRef{Kind: k8s.KindGenericResource, SubKind: s.kind, Namespace: s.namespace, Name: s.name, GVR: s.sourceGVR}), false
		}
		if s.revealed {
			return LoadSecretReveal(c.Background, c.Manager, c.ActiveContext, s.namespace, s.name), false
		}
		return LoadDescribe(c.Background, c.Manager, c.ActiveContext, s.namespace, s.kind, s.name), false

	case km.String() == "ctrl+l":
		s.filter.Clear()
		s.recomputeFilter()
		s.refreshContent()
		return nil, false

	case key.Matches(km, c.Keys.Wrap):
		s.wrap = !s.wrap
		s.vp.SoftWrap = s.wrap
		return nil, false
	}

	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(km)
	return cmd, false
}

func (s *Describe) handleFilterKey(km tea.KeyPressMsg) (tea.Cmd, bool) {
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

func (s *Describe) viewportBlock(c *tuictx.Ctx) string {
	st := components.BlockState{}
	switch {
	case s.loading:
		st.Loading = true
		st.Loading_ = "loading manifest"
	case s.err != nil:
		st.Err = s.err
	case len(s.lines) == 0:
		st.Empty = true
		st.Empty_ = "empty manifest"
	case len(s.filtered) == 0 && s.filter.Value() != "":
		st.NoMatch = true
	default:
		st.Loaded = true
	}
	return components.CenteredViewportBlock(c.Theme, c.SpinnerView(), s.vp, st)
}

// Indicators is the YAML's state for the right end of the tab rule.
func (s *Describe) Indicators() []components.Indicator {
	indicators := make([]components.Indicator, 0, 2)
	if s.filter.Value() != "" {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d/%d lines", len(s.filtered), len(s.lines)),
		})
	} else if len(s.lines) > 0 {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d lines", len(s.lines)),
		})
	}
	indicators = append(indicators, components.Indicator{
		Text: fmt.Sprintf("%.f%%", s.vp.ScrollPercent()*100),
	})
	return indicators
}

// Hints lists the YAML's keys for the footer.
func (s *Describe) Hints() []components.KeyDesc {
	wrapDesc := "wrap"
	if s.wrap {
		wrapDesc = "wrap on"
	}
	return []components.KeyDesc{
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
		{Key: "w", Desc: wrapDesc},
		{Key: "g/G", Desc: "top/bot"},
		{Key: "ctrl+l", Desc: "clear"},
	}
}
