package sections

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Sheet is the describe view of a row, k9s's d: what the resource is and
// how it is doing, what happened to it lately and what it is connected to.
// It opens as a modal over the list the row is on, so it keeps its own
// copies of the sections a resource page's Details tab shows, pointed at
// the row rather than at the page.
type Sheet struct {
	Inspector Inspector
	Activity  Activity
	Connected Connected
	ref       NodeRef
}

// NewSheet returns an empty sheet.
func NewSheet() Sheet {
	return Sheet{Inspector: NewInspector(), Activity: NewActivity(), Connected: NewConnected()}
}

// Ref is the resource the sheet describes.
func (s *Sheet) Ref() NodeRef { return s.ref }

// Open points the sheet at the tree's highlighted row and returns its
// loads. evKind, evNS and evName name the object whose events it lists.
func (s *Sheet) Open(ctx context.Context, mgr *k8s.Manager, contextName string, tr *Tree, evKind, evNS, evName string) tea.Cmd {
	s.ref = tr.SelectedRef()
	cmds := []tea.Cmd{
		s.Inspector.SetSelection(ctx, mgr, contextName, tr),
		s.Connected.SetSelection(ctx, mgr, contextName, tr),
	}
	if evKind == "" || mgr == nil || contextName == "" {
		s.Activity.Clear()
	} else {
		cmds = append(cmds, s.Activity.SetSelection(ctx, mgr, contextName, evKind, evName, evNS))
	}
	return tea.Batch(cmds...)
}

// Status is the described resource's one-line state, for the modal's title.
func (s *Sheet) Status(c *tuictx.Ctx) string {
	full, _ := s.Inspector.Status(c)
	return full
}

// View lays the sheet out the way a page's Details tab is laid out.
func (s *Sheet) View(c *tuictx.Ctx, w, h int) string {
	return RenderDetails(c, w, h, &s.Inspector, &s.Activity, &s.Connected)
}
