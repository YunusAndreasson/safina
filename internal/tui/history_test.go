package tui

import (
	"path/filepath"
	"testing"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/claude"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRecordedOperationDoesNotClaimLaterGeneration(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	cs := fake.NewClientset(&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "original", Generation: 6}})
	mgr := &k8s.Manager{History: store, Clients: map[string]*k8s.Client{"prod": {Clientset: cs}}}
	m := NewModel(mgr, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	op := store.Begin(history.Target{Context: "prod", Kind: "Deployment", Namespace: "shop", Name: "api", UID: "original"}, "restart", nil)
	cmd := m.trackRecordedOperation(sections.WorkloadTarget{Context: "prod", Namespace: "shop", Kind: "Deployment", Name: "api"}, "restart", op, "5")
	msg, ok := cmd().(operationProgressMsg)
	require.True(t, ok)
	require.True(t, msg.superseded)
	m.updateOperation(msg)
	r, err := history.Query(store.Directory(), history.Filter{ID: op.ID})
	require.NoError(t, err)
	require.Equal(t, "superseded", r.Records[len(r.Records)-1].Outcome)
}

func TestHistoryBrowserAndAttachmentAreOffline(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	op := store.Begin(history.Target{Context: "prod", Name: "api"}, "scale", nil)
	op.Record("accepted", "", nil)
	mgr := &k8s.Manager{History: store}
	m := NewModel(mgr, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.ctx.ActiveContext = "prod"
	m.Update(m.openHistory()())
	require.NotNil(t, m.choices)
	require.Equal(t, "history", m.choices.kind)
	require.Len(t, m.choices.model.Items(), 2)
	m.attachHistoryToChat("saved evidence")
	require.True(t, m.chat.HasInput())
	require.False(t, m.chat.Streaming())
	require.Equal(t, stateChatView, m.state)
}

func TestIncidentTimelineSurvivesRestartAndExportsOffline(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	replicas := int32(2)
	controller := true
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "app", Generation: 5, Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"}}, Spec: appsv1.DeploymentSpec{Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}}, Status: appsv1.DeploymentStatus{ObservedGeneration: 5, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded"}}}}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-old", Namespace: "shop", Labels: map[string]string{"app": "api"}, Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"}, OwnerReferences: []metav1.OwnerReference{{UID: "app", Controller: &controller}}}}
	cs := fake.NewClientset(d, rs)
	cs.PrependReactor("get", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() == "scale" {
			return true, &autoscalingv1.Scale{ObjectMeta: d.ObjectMeta}, nil
		}
		return false, nil, nil
	})
	cs.PrependReactor("update", "deployments", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() == "scale" {
			return true, &autoscalingv1.Scale{ObjectMeta: d.ObjectMeta}, nil
		}
		update, ok := a.(ktesting.UpdateAction)
		require.True(t, ok)
		out, ok := update.GetObject().(*appsv1.Deployment)
		require.True(t, ok)
		out = out.DeepCopy()
		out.Generation = 6
		out.Status = appsv1.DeploymentStatus{ObservedGeneration: 6, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}
		require.NoError(t, cs.Tracker().Update(appsv1.SchemeGroupVersion.WithResource("deployments"), out, "shop"))
		return true, out, nil
	})
	mgr := &k8s.Manager{History: store, Clients: map[string]*k8s.Client{"prod": {Clientset: cs}}}
	m := NewModel(mgr, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	_ = sections.LoadDescribe(t.Context(), mgr, "prod", "shop", "Deployment", "api")()
	scale, ok := sections.ScaleWorkload(t.Context(), mgr, "prod", "shop", "Deployment", "api", 2)().(sections.ActionDoneMsg)
	require.True(t, ok)
	require.NoError(t, scale.Err)
	check := m.trackRecordedOperation(*scale.Target, scale.Action, scale.Operation, scale.Generation)
	progress, ok := check().(operationProgressMsg)
	require.True(t, ok)
	m.updateOperation(progress)
	rollback, ok := sections.RollbackDeployment(t.Context(), mgr, "prod", "shop", "api")().(sections.ActionDoneMsg)
	require.True(t, ok)
	require.NoError(t, rollback.Err)
	check = m.trackRecordedOperation(*rollback.Target, rollback.Action, rollback.Operation, rollback.Generation)
	progress, ok = check().(operationProgressMsg)
	require.True(t, ok)
	m.updateOperation(progress)
	require.NoError(t, store.Close())
	failed, err := history.Query(store.Directory(), history.Filter{ID: scale.Operation.ID})
	require.NoError(t, err)
	require.Equal(t, "monitoring_failed", failed.Records[len(failed.Records)-1].Outcome)
	recovered, err := history.Query(store.Directory(), history.Filter{ID: rollback.Operation.ID})
	require.NoError(t, err)
	require.Equal(t, "completed", recovered.Records[len(recovered.Records)-1].Outcome)
	// No manager or cluster is involved after this point.
	require.NoError(t, history.Export(store.Directory(), filepath.Join(t.TempDir(), "incident"), history.Filter{Cluster: "prod"}))
}

func TestHistoryRejectsStaleScopeAndChatEvents(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	mgr := &k8s.Manager{History: store}
	m := NewModel(mgr, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	old := m.openHistory()
	current := m.loadHistory()
	oldMsg, ok := old().(historyLoadedMsg)
	require.True(t, ok)
	m.showHistory(oldMsg)
	require.Empty(t, m.choices.model.Items())
	currentMsg, ok := current().(historyLoadedMsg)
	require.True(t, ok)
	m.showHistory(currentMsg)
	require.NotEmpty(t, m.choices.model.Items())
	oldStream := make(chan claude.Event)
	m.chatStreamCh = oldStream
	require.NotNil(t, m.startChatStream("new prompt"))
	require.Nil(t, m.chatStreamCh)
}

func TestHistoryItemReadsInWords(t *testing.T) {
	target := history.Target{Context: "prod", Namespace: "payments", Kind: "Deployment", Name: "api"}
	scale := historyItem(history.Record{Target: target, Action: "scale", Outcome: "requested", Parameters: map[string]string{"replicas": "3"}})
	require.Contains(t, scale.title, "scale · requested")
	require.Contains(t, scale.detail, "Deployment/api · to 3")
	// before/after carry a workload snapshot, not a requested count.
	after := historyItem(history.Record{Target: target, Action: "restart", Outcome: "after", Parameters: map[string]string{"revision": "5", "ready": "3", "replicas": "3"}})
	require.NotContains(t, after.detail, "to 3")
	require.Contains(t, after.detail, "revision 5 · 3/3 available")
	logs := historyItem(history.Record{Target: target, Action: "logs", Outcome: "capture_ended", Parameters: map[string]string{"previous": "true"}})
	require.Contains(t, logs.title, "logs · capture ended")
	require.Contains(t, logs.detail, "· previous")
	done := historyItem(history.Record{Target: target, Action: "scale", Outcome: "completed", Detail: "3/3 ready"})
	require.Contains(t, done.detail, "· 3/3 ready")
}

// In the chat, ctrl+n starts a new conversation, as the chat's footer says;
// it must not be taken by the notifications shortcut.
func TestChatCtrlNStartsNewConversation(t *testing.T) {
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	m := NewModel(&k8s.Manager{History: store}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.attachHistoryToChat("saved evidence")
	require.Equal(t, stateChatView, m.state)
	m.handleKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	require.Nil(t, m.choices, "ctrl+n in the chat opened notifications")
	require.Equal(t, stateChatView, m.state)
}
