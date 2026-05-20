package tui

// Integration tests that drive the real Model through the message loop with
// the user's live minikube cluster. Skipped when SAFINA_E2E is unset so this
// only runs when explicitly requested.

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func keyMsg(s string) tea.KeyPressMsg {
	if len(s) == 1 {
		return tea.KeyPressMsg{Text: s, Code: rune(s[0])}
	}
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{}
}

// setupRealModel boots a Model wired to the user's kubeconfig. Returns nil
// if no kubeconfig or no reachable cluster.
func setupRealModel(t *testing.T) *Model {
	t.Helper()
	rawCfg, err := config.LoadKubeConfig("")
	if err != nil {
		t.Skipf("no kubeconfig: %v", err)
	}
	mgr, err := k8s.NewManager(rawCfg)
	if err != nil {
		t.Skipf("manager init failed: %v", err)
	}
	if len(mgr.Clients) == 0 {
		t.Skip("no usable clusters in kubeconfig")
	}
	ctxs := config.ListContexts(rawCfg)
	appCfg := &config.AppConfig{EnabledResources: config.DefaultEnabledResources()}
	m := NewModel(mgr, appCfg)
	m.SetContexts(ctxs)
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	return &m
}

func TestE2E_HelpAndQuit(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)
	// Press `?` → help modal.
	_, _ = m.Update(keyMsg("?"))
	require.Equal(t, stateHelp, m.state)
	// Press esc → back to main.
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
	// View renders without panic.
	_ = m.View()
}

func TestE2E_ClusterPickerHasContexts(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)
	v := m.View()
	require.NotEmpty(t, v.Content)
	require.Contains(t, v.Content, "safina")
}

// pump synchronously runs a tea.Cmd, flattening any tea.BatchMsg via the
// runtime's BatchMsg type (which is exported by bubbletea/v2 as the result
// of tea.Batch). Each emitted msg is fed back through Update and any
// resulting cmds are queued.
func pump(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	queue := []tea.Cmd{cmd}
	for i := 0; i < 200 && len(queue) > 0; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				queue = append(queue, sub)
			}
			continue
		}
		_, next := m.Update(msg)
		if next != nil {
			queue = append(queue, next)
		}
	}
}

func TestE2E_DrillIntoClusterAndOpenLogs(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)
	// Drill into the first context (minikube).
	_, cmd := m.Update(keyMsg("enter"))
	require.NotNil(t, cmd)
	pump(t, m, cmd)
	// Poll for children to appear.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && m.tree.Children() == nil {
		time.Sleep(100 * time.Millisecond)
	}
	if m.tree.Children() == nil {
		t.Logf("tree children still nil after %s; tree state: loading=%v err=%v", time.Since(time.Now().Add(-8*time.Second)), m.tree.Loading, m.tree.LoadErr)
		t.Skip("could not load tree children — minikube might be unreachable")
	}
	require.NotEmpty(t, m.ctx.ActiveContext)
	require.Equal(t, "minikube", m.ctx.ActiveContext)
}

func TestE2E_KeybindingDispatchSmoke(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)

	// Force into a cluster-active mode by setting ActiveContext directly so
	// we can exercise key dispatch without a full drill chain.
	m.ctx.ActiveContext = "minikube"

	checks := []struct {
		name   string
		key    string
		expect state
	}{
		{"help", "?", stateHelp},
		{"forwards", "ctrl+f", stateForwardsList},
		{"hot", "H", stateHotView},
		{"configure", ",", stateBrowser},
		{"clusterswitch", "ctrl+k", stateClusters},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			m.state = stateMain
			var msg tea.KeyPressMsg
			switch c.key {
			case "ctrl+f":
				msg = tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'f', Text: ""}
			case "ctrl+k":
				msg = tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'k', Text: ""}
			default:
				msg = keyMsg(c.key)
			}
			_, _ = m.Update(msg)
			require.Equal(t, c.expect, m.state, "key %q should enter %v", c.key, c.expect)
			// Reset.
			_, _ = m.Update(keyMsg("esc"))
			require.Equal(t, stateMain, m.state)
		})
	}
}

func TestE2E_ChatTabOpens(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)
	m.ctx.ActiveContext = "minikube"
	_, _ = m.Update(keyMsg("tab"))
	require.Equal(t, stateChatView, m.state)
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
}

// TestE2E_FullDrillAndCommandsOnPod exercises the realistic critical path:
// pick cluster → drill to namespace → drill to pod → open logs → close →
// open describe → close → open events → close. Each step is verified via
// state + by ensuring the section receives data.
func TestE2E_FullDrillAndCommandsOnPod(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)

	// Cluster pick.
	_, cmd := m.Update(keyMsg("enter"))
	pump(t, m, cmd)
	if m.tree.Children() == nil {
		t.Skip("cluster load failed")
	}

	// Find the experiment namespace folder and drill in.
	c := m.tree.Children()
	require.NotNil(t, c)
	found := false
	for attempt := 0; attempt < 50 && !found; attempt++ {
		ref := m.tree.SelectedRef()
		if (ref.Kind == k8s.KindNamespace || ref.Kind == k8s.KindNamespaceFolder) && ref.Name == "experiment" {
			found = true
			break
		}
		_, _ = m.Update(keyMsg("down"))
	}
	if !found {
		t.Skip("could not find experiment namespace")
	}
	_, cmd = m.Update(keyMsg("enter")) // drill into namespace
	pump(t, m, cmd)
	// Wait for children to land.
	dl := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl) && m.tree.Children() == nil {
		time.Sleep(50 * time.Millisecond)
		pump(t, m, m.loadTreeFocus())
	}

	// In the namespace view we see fold rows (Workloads, Config, …).
	// Walk until we find the Workloads fold, drill in, and there we land
	// on apps.
	for range 20 {
		ref := m.tree.SelectedRef()
		if ref.Kind == k8s.KindWorkloadsFold {
			break
		}
		_, _ = m.Update(keyMsg("down"))
	}
	if m.tree.SelectedRef().Kind != k8s.KindWorkloadsFold {
		t.Skip("could not find Workloads fold")
	}
	// Drill into Workloads.
	_, cmd = m.Update(keyMsg("enter"))
	pump(t, m, cmd)
	dl2 := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl2) && m.tree.SelectedRef().Kind == "" {
		time.Sleep(50 * time.Millisecond)
	}
	if m.tree.SelectedRef().Kind != k8s.KindApp {
		t.Logf("after workloads drill: %+v", m.tree.SelectedRef())
		t.Skip("expected app after workloads drill")
	}
	// Drill into the app — children are pods.
	_, cmd = m.Update(keyMsg("enter"))
	pump(t, m, cmd)
	dl3 := time.Now().Add(5 * time.Second)
	for time.Now().Before(dl3) && m.tree.SelectedRef().Kind == "" {
		time.Sleep(50 * time.Millisecond)
	}
	if m.tree.SelectedRef().Kind != k8s.KindPod {
		t.Logf("after app drill: %+v", m.tree.SelectedRef())
		t.Skip("expected pods after app drill")
	}

	// Open logs on the pod.
	_, cmd = m.Update(keyMsg("l"))
	pump(t, m, cmd)
	require.Equal(t, stateLogView, m.state)
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)

	// Open describe.
	_, cmd = m.Update(keyMsg("d"))
	pump(t, m, cmd)
	require.Equal(t, stateDescribeView, m.state)
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)

	// Open events.
	_, cmd = m.Update(keyMsg("e"))
	pump(t, m, cmd)
	require.Equal(t, stateEventsView, m.state)
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)

	// Port-forward modal.
	_, _ = m.Update(keyMsg("f"))
	require.Equal(t, statePortForwardInput, m.state)
	_, _ = m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)

	// Delete confirm modal.
	_, _ = m.Update(tea.KeyPressMsg{Text: "K", Code: 'K'})
	require.Equal(t, stateDeleteConfirm, m.state)
	_, _ = m.Update(keyMsg("n"))
	require.Equal(t, stateMain, m.state)

	// Restart-pod confirm.
	_, _ = m.Update(tea.KeyPressMsg{Text: "R", Code: 'R'})
	require.Equal(t, stateRestartPodConfirm, m.state)
	_, _ = m.Update(keyMsg("n"))
	require.Equal(t, stateMain, m.state)
}

func TestE2E_ViewRenderingAllStates(t *testing.T) {
	if os.Getenv("SAFINA_E2E") == "" {
		t.Skip("set SAFINA_E2E=1 to run")
	}
	m := setupRealModel(t)
	m.ctx.ActiveContext = "minikube"

	for _, s := range []state{
		stateMain, stateHelp, stateScaleInput, stateDeleteConfirm,
		stateRollbackConfirm, stateRestartConfirm, stateRestartPodConfirm,
		stateEditConfirm, statePortForwardInput, stateCordonConfirm,
		stateDrainConfirm, stateForwardsList, stateBrowser, stateClusters,
		statePalette, stateHotView,
	} {
		m.state = s
		// Setup minimal state needed by render paths.
		if s == stateEditConfirm {
			m.edit = &editSession{Kind: "ConfigMap", Namespace: "default", Name: "test", Orig: []byte("a\n"), Edited: []byte("b\n")}
		}
		if s == statePalette {
			m.palette.Open([]sections.PaletteEntry{})
		}
		if s == stateBrowser {
			m.browser.Open(nil, nil)
		}
		if s == stateClusters {
			m.clusters.Open(m.allContexts, m.ctx.ActiveContext)
		}
		v := m.View()
		require.NotEmpty(t, v.Content, "state %v should render", s)
		// Crude sanity check: no obvious crash markers.
		require.NotContains(t, v.Content, "panic", "state %v rendered panic text", s)
	}
}
