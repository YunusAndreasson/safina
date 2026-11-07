package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/broker"
	"github.com/yunus/safina/internal/broker/api"
	"github.com/yunus/safina/internal/broker/operator"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/history"
	inv "github.com/yunus/safina/internal/investigation"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/mcpserver"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func incomingModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.ctx.Width = 60
	m.ctx.Height = 18
	return m
}

func TestIncomingReviewRequiresDiffOverrideAndFinalApproval(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	m.incoming.mode = "review"
	m.incoming.review = &inv.Review{Before: 2, After: 3, RequiredOverrides: []string{"hpa"}, Diff: "- replicas: 2\n+ replicas: 3"}
	m.handleIncoming(keyMsg("a"))
	require.Equal(t, "review", m.incoming.mode)
	require.Contains(t, m.incoming.err, "diff")
	m.handleIncoming(keyMsg("d"))
	m.handleIncoming(keyMsg("a"))
	require.Equal(t, "review", m.incoming.mode)
	require.Contains(t, m.incoming.err, "override")
	m.handleIncoming(keyMsg("o"))
	_, cmd := m.handleIncoming(keyMsg("a"))
	require.Nil(t, cmd)
	require.Equal(t, "approve", m.incoming.mode)
	m.handleIncoming(keyMsg("n"))
	require.Equal(t, "review", m.incoming.mode)
	m.incoming.review.Noop = true
	m.handleIncoming(keyMsg("a"))
	require.Equal(t, "review", m.incoming.mode)
	m.incoming.review.Noop = false
	m.handleIncoming(keyMsg("e"))
	m.incoming.edit = "2147483648"
	// Numeric validation is independent of a live broker.
	m.incoming.client = broker.NewOperatorClient(broker.Options{})
	_, cmd = m.handleIncoming(keyMsg("enter"))
	require.Nil(t, cmd)
	require.Contains(t, m.incoming.err, "integer")
	m.incoming.review = &inv.Review{Diff: strings.Repeat("\x1b[2Juntrusted\r\u202e\n", 60) + "FINAL LINE", After: 3}
	m.incoming.mode = "review"
	m.incoming.tab = 1
	frame := m.renderIncoming()
	require.LessOrEqual(t, len(strings.Split(frame, "\n")), m.ctx.Height)
	for _, line := range strings.Split(frame, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), m.ctx.Width)
	}
	require.NotContains(t, frame, "\x1b[2J")
	require.NotContains(t, frame, "\u202e")
	require.NotContains(t, frame, "\r")
	m.incoming.viewport.GotoBottom()
	require.Contains(t, m.renderIncoming(), "FINAL LINE")
}

func TestIncomingIgnoresLateReviewAfterClose(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	epoch := m.incoming.request
	m.handleIncoming(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
	m.incomingResult(incomingResultMsg{request: epoch, review: &inv.Review{}})
	require.Nil(t, m.incoming.review)
}

// Acceptance uses the real TUI key/update path and Unix operator API. The HTTP
// fixture supplies API acceptance separately from later controller readiness.
func TestIncomingApproveCloseReopenObservesBrokerOutcome(t *testing.T) {
	for _, kind := range []string{"scale", "image", "restart", "rollback"} {
		t.Run(kind, func(t *testing.T) { incomingApprovalScenario(t, kind) })
	}
}

func incomingApprovalScenario(t *testing.T, kind string) {
	t.Helper()
	var mu sync.Mutex
	replicas := int32(2)
	deployment := appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "original", ResourceVersion: "10", Generation: 4}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}}
	deployment.Spec.Template = corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "api:v1"}}}}
	rollbackTemplate := *deployment.Spec.Template.DeepCopy()
	rollbackTemplate.Spec.Containers[0].Image = "api:v0"
	writes := 0
	cluster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "horizontalpodautoscalers") {
			_ = json.NewEncoder(w).Encode(autoscalingv2.HorizontalPodAutoscalerList{TypeMeta: metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscalerList"}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/replicasets") {
			_ = json.NewEncoder(w).Encode(appsv1.ReplicaSetList{TypeMeta: metav1.TypeMeta{Kind: "ReplicaSetList", APIVersion: "apps/v1"}, Items: []appsv1.ReplicaSet{{ObjectMeta: metav1.ObjectMeta{Name: "old", UID: "old-rs", ResourceVersion: "rs1", Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"}, OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", UID: "original", Controller: new(true)}}}, Spec: appsv1.ReplicaSetSpec{Template: rollbackTemplate}}}})
			return
		}
		if r.Method == http.MethodPatch {
			writes++
			var patch []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || len(patch) != 3 || patch[0]["value"] != "original" || patch[1]["value"] != "10" {
				w.WriteHeader(409)
				return
			}
			value, _ := json.Marshal(patch[2]["value"])
			switch kind {
			case "scale":
				_ = json.Unmarshal(value, &deployment.Spec.Replicas)
			case "image":
				_ = json.Unmarshal(value, &deployment.Spec.Template.Spec.Containers[0].Image)
			case "restart":
				_ = json.Unmarshal(value, &deployment.Spec.Template.Annotations)
			case "rollback":
				deployment.Spec.Template = corev1.PodTemplateSpec{}
				_ = json.Unmarshal(value, &deployment.Spec.Template)
			}
			deployment.ResourceVersion = "11"
			deployment.Generation = 5
		}
		_ = json.NewEncoder(w).Encode(deployment)
	}))
	t.Cleanup(cluster.Close)
	root := t.TempDir()
	runtimeDir, err := os.MkdirTemp("", "safina-ui-") //nolint:usetesting // Unix socket paths must fit platform limits.
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(runtimeDir)) })
	o := broker.Options{RuntimeDir: runtimeDir, StateDir: filepath.Join(root, "state"), Kubeconfig: filepath.Join(root, "config"), History: history.Options{Directory: filepath.Join(root, "history")}}
	require.NoError(t, os.WriteFile(o.Kubeconfig, []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: %s\ncontexts:\n- name: explicit\n  context:\n    cluster: cluster\n    user: unused\n", cluster.URL)), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- broker.Serve(ctx, o) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	require.Eventually(t, func() bool { _, e := broker.BrokerStatus(t.Context(), o); return e == nil }, 3*time.Second, 10*time.Millisecond)
	agent := broker.NewClient(o)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	mcpServer := mcpserver.New(agent)
	serverSession, err := mcpServer.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "tui-acceptance", Version: "1"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = clientSession.Close() }()
	started := incomingMCPCall[api.View](t, clientSession, "start_investigation", api.StartRequest{Session: "claude", Key: "start", Context: "explicit", Summary: "Investigate reviewed change"})
	action := inv.Action{Kind: kind}
	switch kind {
	case "scale":
		action.Replicas = new(int32(3))
	case "image":
		action.Container = "api"
		action.Image = "api:v2"
	case "rollback":
		action.Revision = 1
	}
	incomingMCPCall[api.Submitted](t, clientSession, "submit_proposals", api.SubmitRequest{Session: "claude", Key: "submit", Investigation: started.Investigation.ID, Alternatives: []inv.Alternative{{Target: inv.Target{Cluster: started.Investigation.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", UID: "original"}, Observed: inv.Observed{ResourceVersion: "10", Generation: 4, Time: time.Now()}, Action: action}}})
	first := incomingModel(t)
	first.SetBrokerOptions(o)
	first.Update(first.openIncoming()())
	require.Equal(t, 1, first.incoming.inbox.Pending)
	_, cmd := first.handleIncoming(keyMsg("enter"))
	require.NotNil(t, cmd)
	first.Update(cmd())
	require.Equal(t, int32(2), first.incoming.review.Before)
	if kind == "scale" {
		require.Equal(t, int32(3), first.incoming.review.After)
	} else {
		require.Equal(t, kind, first.incoming.review.Action.Kind)
	}
	first.handleIncoming(keyMsg("d"))
	require.Contains(t, first.renderIncoming(), "COMPLETE EXECUTABLE DIFF")
	first.handleIncoming(keyMsg("a"))
	require.Equal(t, "approve", first.incoming.mode)
	_, cmd = first.handleIncoming(keyMsg("y"))
	require.NotNil(t, cmd)
	result := cmd()
	first.handleIncoming(keyMsg("esc"))
	first.ctx.Cancel()   // TUI closes, broker context survives.
	first.Update(result) // Late approval response cannot reopen a closed review.
	mu.Lock()
	n := *deployment.Spec.Replicas
	deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 5, Replicas: n, UpdatedReplicas: n, AvailableReplicas: n}
	mu.Unlock()
	require.Eventually(t, func() bool {
		view, e := agent.Get(t.Context(), api.GetRequest{Investigation: started.Investigation.ID})
		return e == nil && len(view.Investigation.Executions) == 1 && view.Investigation.Executions[0].State == "completed"
	}, 4*time.Second, 20*time.Millisecond)
	resultView := incomingMCPCall[api.View](t, clientSession, "get_investigation", api.GetRequest{Investigation: started.Investigation.ID})
	require.Equal(t, "completed", resultView.Investigation.Executions[0].State)
	reopened := incomingModel(t)
	reopened.SetBrokerOptions(o)
	reopened.Update(reopened.openIncoming()())
	require.Zero(t, reopened.incoming.inbox.Pending)
	_, detailCmd := reopened.handleIncoming(keyMsg("enter"))
	require.NotNil(t, detailCmd)
	reopened.Update(detailCmd())
	require.Equal(t, "outcome", reopened.incoming.mode)
	require.Contains(t, reopened.incomingSummary(), "Outcome: completed")
	require.Contains(t, reopened.incomingSummary(), "Application recovery unverified")
	mu.Lock()
	require.Equal(t, 1, writes)
	mu.Unlock()
}

var _ operatorAPI = (*broker.OperatorClient)(nil)

func TestIncomingTemplateReviewEditingAndEvidence(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	m.incoming.client = broker.NewOperatorClient(broker.Options{})
	for _, a := range []inv.Action{{Kind: "image", Container: "api", Image: "api:v2"}, {Kind: "rollback", Revision: 7}, {Kind: "restart"}} {
		m.incoming.mode = "review"
		m.incoming.review = &inv.Review{Action: a, Diff: "complete template diff", History: "previous failed action"}
		m.handleIncoming(keyMsg("e"))
		switch a.Kind {
		case "image":
			require.Equal(t, "api=api:v2", m.incoming.edit)
		case "rollback":
			require.Equal(t, "7", m.incoming.edit)
		case "restart":
			require.Equal(t, "review", m.incoming.mode)
			require.Contains(t, m.incoming.err, "no editable")
		}
	}
	m.incoming.mode = "review"
	m.incoming.tab = 2
	m.incoming.selected.Investigation = "one"
	m.incoming.inbox.Investigations = []inv.Investigation{{ID: "one", Evidence: []inv.Evidence{{ID: "proof", Kind: "logs", Text: "untrusted captured text"}}}}
	require.Contains(t, m.renderIncoming(), "proof")
	require.Contains(t, m.renderIncoming(), "untrusted captured text")
}

func incomingMCPCall[Out any](t *testing.T, session *mcp.ClientSession, name string, arguments any) Out {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	require.False(t, result.IsError)
	b, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out Out
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func TestIncomingPromptsRemainVisibleAboveLongSummary(t *testing.T) {
	for _, tc := range []struct{ key, text string }{
		{"e", "revision): 4"},
		{"a", "EXECUTE this exact reviewed change once?"},
		{"x", "Reject this proposal revision?"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			m := incomingModel(t)
			m.openIncoming()
			m.incoming.mode = "review"
			m.incoming.review = &inv.Review{After: 4, Diff: strings.Repeat("diff line\n", 100)}
			m.incoming.inbox.Investigations = []inv.Investigation{{ID: "trial", Summary: strings.Repeat("Long rationale\n", 100)}}
			m.incoming.selected.Investigation = "trial"
			m.handleIncoming(keyMsg("d"))
			m.renderIncoming()
			m.incoming.viewport.GotoBottom()
			m.handleIncoming(keyMsg(tc.key))
			require.Contains(t, ansi.Strip(m.renderIncoming()), tc.text)
		})
	}
}

func TestIncomingDiffRevealsInvisibleLiveValues(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	m.incoming.mode = "review"
	m.incoming.tab = 1
	m.incoming.review = &inv.Review{Diff: "- image: nginx:1.27-\u202ealpine\n+ image: nginx:1.27-alpine\n- note: \x1b[2J"}
	frame := ansi.Strip(m.renderIncoming())
	require.Contains(t, frame, "nginx:1.27-\\u202ealpine")
	require.Contains(t, frame, "\\u001b[2J")
	require.NotContains(t, frame, "\u202e")
	require.NotContains(t, frame, "\x1b[2J")
}

func TestIncomingInboxRefreshKeepsSelectionAndClearsOnlyNetworkError(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	row := func(id string) inv.Investigation {
		return inv.Investigation{ID: id, Proposals: []inv.Proposal{{ID: id, Revision: 1, State: "pending"}}}
	}
	m.incoming.inbox.Investigations = []inv.Investigation{row("selected")}
	m.incoming.inboxErr = "network unavailable"
	m.incoming.err = "Review the diff first"
	m.incoming.inboxRequest = 2
	m.Update(incomingInboxMsg{request: 1, inbox: operator.Inbox{}})
	require.Len(t, m.incoming.inbox.Investigations, 1)
	m.Update(incomingInboxMsg{request: 2, inbox: operator.Inbox{Investigations: []inv.Investigation{row("inserted"), row("selected")}}})
	require.Equal(t, 1, m.incoming.cursor)
	require.Empty(t, m.incoming.inboxErr)
	require.Equal(t, "Review the diff first", m.incoming.err)
}

func TestIncomingDetailIgnoresWrongSelection(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	m.incoming.selected.Investigation = "selected"
	m.incoming.detail = &inv.Investigation{ID: "selected", Summary: "current"}
	m.incomingResult(incomingResultMsg{request: m.incoming.request, detail: &inv.Investigation{ID: "old", Summary: "stale"}, detailMode: "outcome"})
	require.Equal(t, "current", m.incoming.detail.Summary)
}

func TestIncomingSlowInboxIsNotStarvedByPolling(t *testing.T) {
	m := incomingModel(t)
	m.incoming.client = &slowInbox{pending: 7}
	first := m.loadIncoming()
	require.NotNil(t, first)
	// A five-second tick arrives while the first request is still in flight.
	require.Nil(t, m.loadIncoming(), "do not replace a slow request on every tick")
	m.Update(first())
	require.Equal(t, 7, m.incoming.inbox.Pending)
	require.NotNil(t, m.loadIncoming(), "polling must resume after completion")
}

type slowInbox struct {
	operatorAPI
	pending int
}

func (s *slowInbox) Inbox(context.Context) (operator.Inbox, error) {
	return operator.Inbox{Pending: s.pending}, nil
}

func TestIncomingDetailFailureClearsBusy(t *testing.T) {
	m := incomingModel(t)
	m.openIncoming()
	m.incoming.client = &failedDetail{}
	m.incoming.selected.Investigation = "selected"
	m.incoming.mode = "outcome"
	cmd := m.incomingCommand("details")
	m.Update(cmd())
	require.False(t, m.incoming.busy)
	require.Contains(t, m.incoming.err, "detail unavailable")
}

type failedDetail struct{ operatorAPI }

func (*failedDetail) Investigation(context.Context, string) (inv.Investigation, error) {
	return inv.Investigation{}, errors.New("detail unavailable")
}
