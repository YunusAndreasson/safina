package tui

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Every command is held pending. Only explicitly tagged, simulated read
// replies are delivered. No API calls, subprocesses or write actions run.
func FuzzNavigationTransitions(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27})
	f.Add([]byte{9, 2, 2, 3, 0, 0, 4, 8, 0, 22, 23, 0, 0, 13, 4, 4, 2, 0, 0})
	f.Add([]byte{20, 6, 20, 3, 6, 10, 6, 0, 22, 23, 0})
	f.Fuzz(navigationTransitionCase)
}

func navigationTransitionCase(t *testing.T, data []byte) {
	t.Helper()
	if len(data) > 128 {
		data = data[:128]
	}
	model := NewModel(&k8s.Manager{Forwards: k8s.NewForwardManager()}, &config.AppConfig{})
	m := &model
	m.configWriter = func(*config.AppConfig) error { return nil }
	t.Cleanup(m.ctx.Cancel)
	m.SetContexts([]config.ContextInfo{{Name: "demo"}})
	m.ctx.ActiveContext = "demo"
	m.tree.PushContext("demo")
	m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Namespace: "shop", Name: "shop"})
	m.tree.Push(sections.NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
	ch := &sections.Children{Kind: k8s.KindApp}
	for n := range 24 {
		ch.Apps = append(ch.Apps, k8s.AppInfo{Kind: k8s.AppKindDeployment, Name: fmt.Sprintf("api-%02d", n), Namespace: "shop", Ready: "2/2", Health: k8s.HealthHealthy})
	}
	m.tree.SetChildren(m.tree.Path(), ch)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	pending := []resourceViewResultMsg{}
	keys := []tea.KeyPressMsg{keyMsg("esc"), keyMsg("tab"), keyMsg("down"), keyMsg("up"), {Code: tea.KeyLeft}, {Code: tea.KeyRight}, keyMsg("enter"), keyMsg("?"), keyMsg("/"), keyMsg(":"), {Code: 'p', Mod: tea.ModCtrl}, keyMsg("z"), keyMsg("d"), keyMsg("e"), keyMsg("m"), keyMsg("l"), keyMsg("a"), keyMsg("i"), {Code: tea.KeyTab, Mod: tea.ModShift}, {Code: 'k', Mod: tea.ModCtrl}, {Code: 'b', Mod: tea.ModCtrl}, {Code: 'f', Mod: tea.ModCtrl}}
	m.View()
	for step, b := range data {
		beforeD, beforeE := m.viewRequests[stateDescribeView], m.viewRequests[stateEventsView]
		switch int(b) % 28 {
		case 22:
			sizes := [][2]int{{50, 14}, {60, 18}, {80, 24}, {120, 36}, {160, 50}, {40, 10}}
			s := sizes[int(b/28)%len(sizes)]
			m.Update(tea.WindowSizeMsg{Width: s[0], Height: s[1]})
		case 23:
			m.Update(tea.MouseClickMsg{X: int(b) % max(1, m.ctx.Width), Y: step % max(1, m.ctx.Height), Button: tea.MouseLeft})
		case 24:
			if len(pending) > 0 {
				n := int(b) % len(pending)
				reply := pending[n]
				pending = append(pending[:n], pending[n+1:]...)
				m.Update(reply)
			}
		case 25:
			if m.tree.Focus().Kind == k8s.KindWorkloadsFold {
				next := &sections.Children{Kind: k8s.KindApp, Apps: append([]k8s.AppInfo(nil), ch.Apps...)}
				if step%2 == 0 {
					next.Apps[0], next.Apps[1] = next.Apps[1], next.Apps[0]
				}
				m.Update(sections.TreeChildrenLoadedMsg{Path: m.tree.Path(), Children: next})
			}
		case 26:
			m.Update(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
		case 27:
			m.Update(tea.PasteMsg{Content: "api"})
		default:
			k := keys[int(b)%28]
			// Preferences use the fixture's in-memory writer. Keep chat submissions
			// local as well; all other returned commands remain held.
			if m.state == stateChatView && !m.treeFocusInImmersive && k.Code == tea.KeyEnter {
				k = keyMsg("tab")
			}
			m.Update(k)
		}
		if m.viewRequests[stateDescribeView] > beforeD {
			pending = append(pending, resourceViewResultMsg{view: stateDescribeView, request: m.viewRequests[stateDescribeView], cluster: m.ctx.ActiveContext, result: sections.DescribeLoadedMsg{YAML: "source: " + m.describe.PaneTitle()}})
		}
		if m.viewRequests[stateEventsView] > beforeE {
			pending = append(pending, resourceViewResultMsg{view: stateEventsView, request: m.viewRequests[stateEventsView], cluster: m.ctx.ActiveContext, result: sections.EventsLoadedMsg{Events: []k8s.EventInfo{{Reason: "Started", Message: m.events.PaneTitle()}}}})
		}
		frame := m.View().Content
		require.LessOrEqual(t, len(strings.Split(frame, "\n")), m.ctx.Height, "step %d state %v input %v", step, m.state, data[:step+1])
		for _, row := range strings.Split(frame, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(row), m.ctx.Width, "step %d state %v input %v", step, m.state, data[:step+1])
		}
		require.NotEmpty(t, m.tree.Path())
	}
}

// Runs without compiler instrumentation, including on hosts whose installed
// native terminal headers cannot be rebuilt with Go's fuzz instrumentation.
// Commands remain held, so the soak cannot mutate a cluster or launch a shell.
func TestNavigationSoak(t *testing.T) {
	raw := os.Getenv("SAFINA_NAV_SOAK_DURATION")
	if raw == "" {
		t.Skip("set SAFINA_NAV_SOAK_DURATION, e.g. 3m, for the randomized soak")
	}
	duration, err := time.ParseDuration(raw)
	require.NoError(t, err)
	require.Greater(t, duration, time.Duration(0))
	const seed = int64(0x534146494e41)
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // Reproducible QA input, not security data.
	start := time.Now()
	cases, events := 0, 0
	for time.Since(start) < duration {
		data := make([]byte, 32+rng.Intn(97))
		_, err := rng.Read(data)
		require.NoError(t, err)
		if !t.Run(fmt.Sprintf("case-%06d", cases), func(t *testing.T) { navigationTransitionCase(t, data) }) {
			break
		}
		cases++
		events += len(data)
	}
	t.Logf("seed=%d cases=%d events=%d elapsed=%s", seed, cases, events, time.Since(start).Round(time.Millisecond))
}
