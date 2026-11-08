package tui

import (
	"errors"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestErrorPersistsAndHistoryKeepsSuccess(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	require.Nil(t, m.raiseError("permission denied"))
	m.raiseStatus("copied")
	m.Update(clearBannerMsg{epoch: m.bannerEpoch})
	require.Equal(t, "permission denied", m.bannerMsg)
	require.Len(t, m.notifications, 2)
	m.dismissBanner()
	require.Empty(t, m.bannerMsg)
	m.openNotifications()
	require.Len(t, m.choices.model.Items(), 2)
}

func TestChoiceDialogsFitAndDoNotEditUnderlyingInput(t *testing.T) {
	for _, size := range [][2]int{{50, 14}, {80, 24}, {120, 40}} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.state = statePalette
		m.palette.SetQuery("keep")
		m.openChoices("logs", "Choose log target", "enter current logs · p previous logs · esc cancel", []list.Item{choiceItem{title: "café 東京", detail: "Waiting:CrashLoopBackOff"}})
		m.handlePaste(tea.PasteMsg{Content: "changed"})
		require.Equal(t, "keep", m.palette.Query())
		card := m.choicesCard()
		require.LessOrEqual(t, lipgloss.Width(card), size[0])
		require.LessOrEqual(t, lipgloss.Height(card), size[1])
		m.handleChoices(keyMsg("esc"))
		require.Nil(t, m.choices)
		require.Equal(t, statePalette, m.state)
	}
}

func TestLogTargetsExposePreviousAndIgnoreLateLoads(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}}}, Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", RestartCount: 2, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}, {Name: "sidecar", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
	mgr := &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Clientset: fake.NewClientset(pod)}}}
	m := NewModel(mgr, &config.AppConfig{})
	m.ctx.ActiveContext = "demo"
	cmd := m.openLogTargets(sections.NodeRef{Kind: k8s.KindPod, Name: "web", Namespace: "shop"})
	msg, ok := cmd().(logTargetsMsg)
	require.True(t, ok)
	require.NoError(t, msg.err)
	require.Len(t, msg.targets, 2)
	require.Equal(t, "app", msg.targets[0].container)
	require.True(t, msg.targets[0].previous)
	m.handleChoices(keyMsg("esc"))
	m.setLogTargets(msg)
	require.Nil(t, m.choices)
}

func TestOperationProgressRejectsOldResponseAndReportsFailure(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.ctx.ActiveContext = "demo"
	target := sections.WorkloadTarget{Context: "demo", Namespace: "shop", Kind: k8s.AppKindDeployment, Name: "web"}
	m.trackOperation(target, "restart")
	var key string
	var op operation
	for key, op = range m.operations {
		break
	}
	require.Nil(t, m.updateOperation(operationProgressMsg{key: key, revision: op.revision - 1, done: true}))
	require.Len(t, m.operations, 1)
	require.NotNil(t, m.updateOperation(operationProgressMsg{key: key, revision: op.revision, text: "1/3 ready"}))
	require.Contains(t, m.operationSummary(), "1/3 ready")
	m.updateOperation(operationProgressMsg{key: key, revision: op.revision, err: errors.New("deadline exceeded")})
	require.Empty(t, m.operations)
	require.Contains(t, m.bannerMsg, "deadline exceeded")
	m.dismissBanner()
	m.operations[key] = operation{target: target, action: "scale", started: time.Now().Add(-6 * time.Minute), revision: 3}
	m.updateOperation(operationProgressMsg{key: key, revision: 3, text: "0/3 ready"})
	require.Contains(t, m.bannerMsg, "still pending")
}

func TestNamespaceSearchDoesNotReplaceNewPaletteSession(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.state = statePalette
	m.ctx.ActiveContext = "demo"
	m.searchRequest = 2
	m.palette.Open([]sections.PaletteEntry{{Name: "keep"}})
	m.Update(sections.NamespaceSearchMsg{Request: 1, Context: "demo", Entries: []sections.PaletteEntry{{Name: "old"}}})
	selected, ok := m.palette.Selected()
	require.True(t, ok)
	require.Equal(t, "keep", selected.Name)
}

func TestOpeningTargetPickerDoesNotInvalidateCurrentStream(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.logRequest = 7
	m.openLogTargets(sections.NodeRef{Kind: k8s.KindPod, Name: "web", Namespace: "shop"})
	require.EqualValues(t, 7, m.logRequest)
	m.handleChoices(keyMsg("esc"))
	require.EqualValues(t, 7, m.logRequest)
}
