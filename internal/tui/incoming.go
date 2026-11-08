package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yunus/safina/internal/broker"
	"github.com/yunus/safina/internal/broker/operator"
	inv "github.com/yunus/safina/internal/investigation"
	"github.com/yunus/safina/internal/tui/components"
)

type operatorAPI interface {
	Inbox(context.Context) (operator.Inbox, error)
	Investigation(context.Context, string) (inv.Investigation, error)
	Prepare(context.Context, operator.Request) (inv.Review, error)
	Approve(context.Context, operator.Request) (inv.Execution, error)
	Revise(context.Context, operator.Request) (inv.Proposal, error)
	Reject(context.Context, operator.Request) error
}

type incomingState struct {
	client       operatorAPI
	inbox        operator.Inbox
	detail       *inv.Investigation
	inboxErr     string
	inboxRequest uint64
	inboxBusy    bool
	viewport     viewport.Model
	cursor       int
	selected     operator.Request
	review       *inv.Review
	mode         string
	tab          int
	seenDiff     bool
	override     bool
	busy         bool
	request      uint64
	edit         string
	err          string
}

type (
	incomingTickMsg  struct{}
	incomingInboxMsg struct {
		request uint64
		inbox   operator.Inbox
		err     error
	}
)

type incomingResultMsg struct {
	detail     *inv.Investigation
	detailMode string
	request    uint64
	review     *inv.Review
	err        error
}

func (m *Model) SetBrokerOptions(o broker.Options) { m.incoming.client = broker.NewOperatorClient(o) }

func incomingTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return incomingTickMsg{} })
}

func (m *Model) loadIncoming() tea.Cmd {
	if m.incoming.client == nil || m.incoming.inboxBusy {
		return nil
	}
	m.incoming.inboxBusy = true
	client := m.incoming.client
	m.incoming.inboxRequest++
	request := m.incoming.inboxRequest
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		inbox, err := client.Inbox(ctx)
		return incomingInboxMsg{request: request, inbox: inbox, err: err}
	}
}

// Untrusted agent/evidence text cannot inject ANSI, CR, bidi or other terminal
// controls. Newline and tab remain ordinary text for the wrapped viewport.
func incomingText(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, s)
}

// Live templates may contain invisible/control characters even when new typed
// parameters forbid them. Escape them in diffs instead of silently removing
// bytes from the reviewed change. The diff itself contains no trusted ANSI.
func incomingDiffText(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.In(r, unicode.Cf) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (m *Model) openIncoming() tea.Cmd {
	m.state = stateIncoming
	p := &m.incoming
	p.mode = "list"
	p.err = ""
	p.review = nil
	p.request++
	p.busy = false
	p.viewport = viewport.New()
	p.viewport.SoftWrap = true
	return m.loadIncoming()
}

type incomingRow struct {
	investigation inv.Investigation
	proposal      inv.Proposal
}

func (m *Model) incomingRows() []incomingRow {
	var rows []incomingRow
	for _, current := range m.incoming.inbox.Investigations {
		for _, p := range current.Proposals {
			rows = append(rows, incomingRow{current, p})
		}
	}
	return rows
}

func (m *Model) incomingCommand(action string) tea.Cmd {
	p := &m.incoming
	if p.client == nil {
		p.err = "Broker is not configured"
		return nil
	}
	client, request := p.client, p.selected
	detailMode := p.mode
	if p.review != nil && action == "approve" {
		request.Review, request.Digest = p.review.ID, p.review.Digest
		if p.override {
			request.Overrides = append([]string(nil), p.review.RequiredOverrides...)
		}
	}
	if action == "revise" {
		var a inv.Action
		if p.review == nil {
			return nil
		}
		a = p.review.Action
		switch a.Kind {
		case "image":
			container, image, ok := strings.Cut(p.edit, "=")
			if !ok {
				p.err = "Use container=image"
				return nil
			}
			a.Container = container
			a.Image = image
		case "rollback":
			revision, err := strconv.ParseInt(p.edit, 10, 64)
			if err != nil || revision < 1 {
				p.err = "Revision must be a positive integer"
				return nil
			}
			a.Revision = revision
		case "restart":
			p.err = "Restart has no editable fields"
			return nil
		default:
			value, err := strconv.ParseInt(p.edit, 10, 32)
			if err != nil || value < 0 {
				p.err = "Replicas must be an integer from 0 to 2147483647"
				return nil
			}
			a = inv.Action{Kind: "scale", Replicas: new(int32(value))}
		}
		if err := a.Validate(); err != nil {
			p.err = "Invalid action parameters"
			return nil
		}
		request.Action = &a
	}
	p.request++
	epoch := p.request
	p.busy = true
	p.err = ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var err error
		var review *inv.Review
		var detail *inv.Investigation
		switch action {
		case "details":
			// Read-only selected detail; no operator decision.
		case "prepare":
			r, e := client.Prepare(ctx, request)
			review, err = &r, e
		case "approve":
			_, err = client.Approve(ctx, request)
		case "reject":
			err = client.Reject(ctx, request)
		case "revise":
			next, e := client.Revise(ctx, request)
			err = e
			if err == nil {
				request.Revision = next.Revision
				request.Replicas = nil
				request.Action = nil
				r, e := client.Prepare(ctx, request)
				review, err = &r, e
			}
		}
		if err == nil && (action == "prepare" || action == "revise" || action == "details") {
			current, e := client.Investigation(ctx, request.Investigation)
			err = e
			if e == nil {
				detail = &current
			}
		}
		mode := ""
		if action == "details" {
			mode = detailMode
		}
		return incomingResultMsg{request: epoch, review: review, detail: detail, detailMode: mode, err: err}
	}
}

func (m *Model) incomingResult(msg incomingResultMsg) tea.Cmd {
	p := &m.incoming
	if m.state != stateIncoming || msg.request != p.request || (msg.err == nil && msg.detail != nil && msg.detail.ID != p.selected.Investigation) {
		return nil
	}
	p.busy = false
	if msg.err != nil {
		p.err = "Broker: " + msg.err.Error() + ". Refresh and review again; no automatic execution retry."
		p.mode = "list"
		p.review = nil
		return m.loadIncoming()
	}
	if msg.detail != nil {
		p.detail = msg.detail
	}
	if msg.detailMode != "" {
		p.mode = msg.detailMode
		return nil
	}
	if msg.review != nil {
		p.review = msg.review
		p.selected.Revision = msg.review.Revision
		p.mode = "review"
		p.tab = 0
		p.seenDiff = false
		p.override = false
		p.viewport.GotoTop()
		return m.loadIncoming()
	}
	p.mode = "list"
	p.review = nil
	return m.loadIncoming()
}

func (m *Model) handleIncoming(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := &m.incoming
	key := msg.String()
	if key == "esc" {
		if p.mode == "list" || p.busy {
			m.state = stateMain
			p.request++
			p.busy = false
		} else {
			p.mode = "list"
			p.review = nil
		}
		return m, nil
	}
	if p.busy {
		return m, nil
	}
	if p.mode == "outcome" && key == "r" {
		return m, m.incomingCommand("details")
	}
	if p.mode == "edit" {
		switch key {
		case "enter":
			return m, m.incomingCommand("revise")
		case "backspace":
			if len(p.edit) > 0 {
				p.edit = p.edit[:len(p.edit)-1]
			}
		default:
			if len(key) == 1 && key[0] >= 32 && len(p.edit) < 1024 {
				p.edit += key
			}
		}
		return m, nil
	}
	if p.mode == "approve" || p.mode == "reject" {
		if key == "y" {
			return m, m.incomingCommand(p.mode)
		}
		if key == "n" {
			p.mode = "review"
			if p.review == nil {
				p.mode = "list"
			}
		}
		return m, nil
	}
	if p.mode == "list" {
		rows := m.incomingRows()
		switch key {
		case "j", "down":
			p.cursor = min(p.cursor+1, max(0, len(rows)-1))
			p.viewport.GotoTop()
		case "k", "up":
			p.cursor = max(0, p.cursor-1)
			p.viewport.GotoTop()
		case "r":
			return m, m.loadIncoming()
		case "enter":
			if len(rows) == 0 {
				return m, nil
			}
			p.cursor = min(p.cursor, len(rows)-1)
			row := rows[p.cursor]
			p.detail = nil
			p.selected = operator.Request{Investigation: row.investigation.ID, Proposal: row.proposal.ID, Revision: row.proposal.Revision}
			if row.proposal.State != "pending" {
				p.mode = "outcome"
				p.viewport.GotoTop()
				return m, m.incomingCommand("details")
			}
			return m, m.incomingCommand("prepare")
		case "x":
			if len(rows) > 0 {
				row := rows[min(p.cursor, len(rows)-1)]
				if row.proposal.State == "pending" {
					p.detail = nil
					p.selected = operator.Request{Investigation: row.investigation.ID, Proposal: row.proposal.ID, Revision: row.proposal.Revision}
					p.mode = "reject"
					p.viewport.GotoTop()
					return m, m.incomingCommand("details")
				}
			}
		}
		return m, nil
	}
	if p.mode == "review" && p.review != nil {
		switch key {
		case "tab":
			p.tab = (p.tab + 1) % 3
			p.viewport.GotoTop()
			if p.tab == 1 {
				p.seenDiff = true
			}
			return m, nil
		case "d":
			p.tab = 1
			p.seenDiff = true
			p.viewport.GotoTop()
			return m, nil
		case "e":
			p.mode = "edit"
			p.viewport.GotoTop()
			switch p.review.Action.Kind {
			case "image":
				p.edit = p.review.Action.Container + "=" + p.review.Action.Image
			case "rollback":
				p.edit = strconv.FormatInt(p.review.Action.Revision, 10)
			case "restart":
				p.mode = "review"
				p.err = "Restart has no editable fields"
			default:
				p.edit = strconv.FormatInt(int64(p.review.After), 10)
			}
			return m, nil
		case "x":
			p.mode = "reject"
			p.viewport.GotoTop()
			return m, nil
		case "o":
			p.override = !p.override
			return m, nil
		case "r":
			return m, m.incomingCommand("prepare")
		case "A": // a write, so a capital (Barbarossa mode), like every other key that changes the cluster
			switch {
			case p.review.Noop:
				p.err = "No-op: there is no change to approve"
			case !p.seenDiff:
				p.err = "Open the complete diff with d before approval"
			case len(p.review.RequiredOverrides) > 0 && !p.override:
				p.err = "Read the controller warnings and press o to explicitly override"
			default:
				p.mode = "approve"
				p.viewport.GotoTop()
				p.err = ""
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return m, cmd
}

func (m *Model) incomingDetails() []inv.Investigation {
	if d := m.incoming.detail; d != nil && d.ID == m.incoming.selected.Investigation {
		return []inv.Investigation{*d}
	}
	return m.incoming.inbox.Investigations
}

func (m *Model) incomingSummary() string {
	p := &m.incoming
	var text strings.Builder
	for _, current := range m.incomingDetails() {
		if current.ID != p.selected.Investigation {
			continue
		}
		fmt.Fprintf(&text, "Investigation %s\nContext: %s\nCluster: %s\n\nSummary (agent-provided, unverified):\n%s\n", current.ID, current.Context, current.Cluster, current.Summary)
		for _, item := range current.Proposals {
			if item.ID != p.selected.Proposal || item.Revision != p.selected.Revision {
				continue
			}
			a := item.Alternative
			fmt.Fprintf(&text, "\nGroup %s\nProposal %s · revision %d · %s\nDeployment %s/%s\nUID: %s\nAction: %s\nRationale (unverified):\n%s\nExpected verification (unverified):\n%s\n", item.Group, item.ID, item.Revision, item.State, a.Target.Namespace, a.Target.Name, a.Target.UID, a.Action.Kind, a.Rationale, a.Verification)
		}
		for _, decision := range current.Decisions {
			if decision.Proposal != p.selected.Proposal {
				continue
			}
			fmt.Fprintf(&text, "\nDecision: %s by %s · revision %d\nOverrides: %s\n", decision.Choice, decision.Operator, decision.Revision, strings.Join(decision.Overrides, ", "))
			for _, op := range current.Executions {
				if op.Decision == decision.ID {
					fmt.Fprintf(&text, "Outcome: %s\n%s\n", op.State, op.Detail)
				}
			}
		}
	}
	if p.review != nil {
		r := p.review
		fmt.Fprintf(&text, "\nLIVE REVIEW: %s · desired replicas %d → %d\nVersion: %s · generation %d\nObserved: %s\n", r.Action.Kind, r.Before, r.After, r.ResourceVersion, r.Generation, r.Time.Format(time.RFC3339))
		fmt.Fprintf(&text, "\nRecent target history / previous attempts (untrusted):\n%s\n", r.History)
		for _, warning := range r.Warnings {
			fmt.Fprintf(&text, "\nWarning: %s\n", warning)
		}
		fmt.Fprintf(&text, "\nRequired overrides: %s\nOverride acknowledged: %t\n", strings.Join(r.RequiredOverrides, ", "), p.override)
	}
	return text.String()
}

func (m *Model) renderIncoming() string {
	p := &m.incoming
	w, h := m.ctx.Width, m.ctx.Height
	title := fmt.Sprintf("Incoming proposals · %d pending · %s", p.inbox.Pending, p.mode)
	shortcuts := []components.KeyDesc{{Key: "↑↓/j/k", Desc: "select"}, {Key: "↵", Desc: "review/outcome"}, {Key: "x", Desc: "reject", Write: true}, {Key: "r", Desc: "refresh"}, {Key: "esc", Desc: "close"}}
	footerNote := ""
	var body string
	switch p.mode {
	case "list":
		rows := m.incomingRows()
		var b strings.Builder
		for i, row := range rows {
			if i < p.cursor {
				continue
			}
			mark := " "
			if i == p.cursor {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s %s · %s/%s · %s · %s\n  cluster %s · group %s · rev %d\n\n", mark, row.investigation.Context, row.proposal.Alternative.Target.Namespace, row.proposal.Alternative.Target.Name, row.proposal.Alternative.Action.Kind, row.proposal.State, row.investigation.Cluster, row.proposal.Group, row.proposal.Revision)
		}
		if len(rows) == 0 {
			b.WriteString("No proposals. Agents may submit while this TUI is closed.\n")
		}
		body = b.String()
	case "review":
		shortcuts = []components.KeyDesc{{Key: "tab", Desc: "summary/diff/evidence"}, {Key: "d", Desc: "diff"}, {Key: "e", Desc: "edit"}, {Key: "o", Desc: "override"}, {Key: "A", Desc: "approve", Write: true}, {Key: "x", Desc: "reject", Write: true}, {Key: "r", Desc: "refresh"}, {Key: "esc", Desc: "back"}}
		body = m.incomingSummary()
		if p.tab == 1 && p.review != nil {
			body = "COMPLETE EXECUTABLE DIFF\n\n" + incomingDiffText(p.review.Diff)
		}
		if p.tab == 2 {
			var evidenceText strings.Builder
			evidenceText.WriteString("Captured evidence (untrusted observations)\n")
			for _, current := range m.incomingDetails() {
				if current.ID != p.selected.Investigation {
					continue
				}
				if len(current.Evidence) == 0 {
					evidenceText.WriteString("No captured evidence; agent assertions remain unverified.\n")
				}
				for _, e := range current.Evidence {
					fmt.Fprintf(&evidenceText, "\nID %s · %s/%s · %s\nObserved %s · truncated %t\n%s\nWarnings: %s\n", e.ID, e.Namespace, e.Resource, e.Kind, e.Observed.Format(time.RFC3339), e.Truncated, e.Text, strings.Join(e.Warnings, "; "))
				}
			}
			body = evidenceText.String()
		}
	case "outcome":
		body = m.incomingSummary()
		shortcuts = []components.KeyDesc{{Key: "↑↓/pgup/pgdn", Desc: "scroll"}, {Key: "r", Desc: "refresh"}, {Key: "esc", Desc: "back"}}
	case "edit":
		body = "New value (replicas / container=image / revision): " + p.edit + "\n\n" + m.incomingSummary()
		shortcuts = []components.KeyDesc{{Key: "↵", Desc: "save revision"}, {Key: "esc", Desc: "cancel"}}
	case "approve":
		body = "EXECUTE this exact reviewed change once?\nThe broker continues monitoring after Safina closes.\n\n" + m.incomingSummary()
		shortcuts = []components.KeyDesc{{Key: "y", Desc: "approve and execute", Write: true}, {Key: "n/esc", Desc: "cancel"}}
	case "reject":
		body = "Reject this proposal revision?\n\n" + m.incomingSummary()
		shortcuts = []components.KeyDesc{{Key: "y", Desc: "reject", Write: true}, {Key: "n/esc", Desc: "cancel"}}
	}
	if p.busy {
		footerNote = "Broker request in progress; approved execution continues.\n"
		shortcuts = []components.KeyDesc{{Key: "esc", Desc: "close"}}
	}
	if p.inboxErr != "" {
		body = "Inbox unavailable: " + p.inboxErr + "\n\n" + body
	}
	if p.err != "" {
		body = "ERROR: " + p.err + "\n\n" + body
	}
	if len(p.inbox.Warnings) > 0 {
		body = "Recording warnings: " + strings.Join(p.inbox.Warnings, "; ") + "\n\n" + body
	}
	foot := ansi.Wrap(footerNote+components.RenderModalKeysRow(m.ctx.Theme, shortcuts), max(1, w-2), "")
	p.viewport.SetWidth(max(1, w-2))
	p.viewport.SetHeight(max(1, h-3-strings.Count(foot, "\n")))
	p.viewport.SetContent(incomingText(body))
	// A write waiting for its yes wears Barbarossa red, like every other.
	titleStyle := m.ctx.Theme.CyanBold
	if p.mode == "approve" {
		titleStyle = m.ctx.Theme.Danger
	}
	return titleStyle.Render(ansi.Truncate(title, w, "…")) + "\n" + p.viewport.View() + "\n" + foot
}
