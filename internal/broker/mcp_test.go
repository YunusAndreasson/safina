package broker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/broker/api"
	"github.com/YunusAndreasson/safina/internal/broker/operator"
	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/YunusAndreasson/safina/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func callTool[Out any](t *testing.T, session *mcp.ClientSession, name string, args any) Out {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, result.IsError, "%+v", result.Content)
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out Out
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestMCPInvestigatesAndObservesAllOperatorActions(t *testing.T) {
	fixture, cluster, _ := scaleFixture(t)
	cluster.deployment.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
	cluster.deployment.Spec.Template = corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "api:v1", Env: []corev1.EnvVar{{Name: "DO_NOT_EXPOSE", Value: "literal-secret"}}}}}}
	cluster.replicaSets = []appsv1.ReplicaSet{{ObjectMeta: metav1.ObjectMeta{Name: "old", UID: "rs-old", ResourceVersion: "r1", Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"}, OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", UID: "original", Controller: new(true)}}}, Spec: appsv1.ReplicaSetSpec{Template: *cluster.deployment.Spec.Template.DeepCopy()}}}
	cluster.logText = "2026-11-07T10:00:00Z password=supersecret CUSTOM_PRIVATE\n" + strings.Repeat("line\n", 5000)
	runtimeDir, err := os.MkdirTemp("", "safina-mcp-") //nolint:usetesting // Unix socket paths have a small platform-specific limit.
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(runtimeDir)) })
	o := fixture.opts
	o.RuntimeDir = runtimeDir
	o.StateDir = filepath.Join(t.TempDir(), "state")
	o.History.RedactPatterns = []string{"CUSTOM_PRIVATE"}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, o) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	require.Eventually(t, func() bool { _, e := BrokerStatus(t.Context(), o); return e == nil }, 3*time.Second, 10*time.Millisecond)
	client := NewClient(o)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	sdkServer := mcpserver.New(client)
	serverSession, err := sdkServer.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	defer func() { _ = serverSession.Close() }()
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	session, err := sdkClient.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 9)
	for _, tool := range tools.Tools {
		require.NotContains(t, tool.Name, "approve")
		require.NotContains(t, tool.Name, "execute")
		if tool.Name == "submit_proposals" || tool.Name == "start_investigation" {
			require.False(t, tool.Annotations.ReadOnlyHint)
		}
	}
	contexts := callTool[api.Contexts](t, session, "list_contexts", api.Empty{})
	require.Contains(t, contexts.Contexts, "explicit")
	started := callTool[api.View](t, session, "start_investigation", api.StartRequest{Session: "claude", Key: "start", Context: "explicit", Summary: "investigate"})
	require.Contains(t, started.History, "Only retained Safina activity")
	id := started.Investigation.ID
	observed := callTool[api.Observation](t, session, "inspect_workload", api.InspectRequest{Investigation: id, Namespace: "shop", Name: "api"})
	require.NotContains(t, observed.Evidence.Text, "literal-secret")
	require.Contains(t, observed.Evidence.Text, "original")
	logs := callTool[api.Observation](t, session, "read_logs", api.LogsRequest{Investigation: id, Namespace: "shop", Pod: "api-pod", Container: "api", Previous: true})
	require.True(t, logs.Evidence.Truncated)
	require.LessOrEqual(t, len(logs.Evidence.Text), evidenceBytes)
	require.NotContains(t, logs.Evidence.Text, "supersecret")
	require.NotContains(t, logs.Evidence.Text, "CUSTOM_PRIVATE")
	fetched := callTool[api.Observation](t, session, "get_evidence", api.EvidenceRequest{Investigation: id, Evidence: logs.Evidence.ID})
	require.Equal(t, logs.Evidence.Text, fetched.Evidence.Text)
	callTool[api.Observation](t, session, "read_events", api.EventsRequest{Investigation: id, Namespace: "shop", UID: "original"})
	for _, name := range []string{"approve", "execute", "select", "reject", "revise"} {
		result, e := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		require.True(t, e != nil || result.IsError)
	}
	invalid, invalidErr := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_investigation", Arguments: map[string]any{"investigationId": id, "approve": true}})
	require.True(t, invalidErr != nil || invalid.IsError)
	cluster.mu.Lock()
	require.Zero(t, cluster.writes)
	cluster.mu.Unlock()
	operatorClient := NewOperatorClient(o)
	for _, kind := range []string{"scale", "image", "restart", "rollback"} {
		cluster.mu.Lock()
		d := cluster.deployment.DeepCopy()
		cluster.mu.Unlock()
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
		a := inv.Alternative{Target: inv.Target{Cluster: started.Investigation.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", UID: "original"}, Observed: inv.Observed{ResourceVersion: d.ResourceVersion, Generation: d.Generation, Time: time.Now()}, Action: action, Evidence: []string{observed.Evidence.ID}, Rationale: "captured evidence supports investigation"}
		submitted := callTool[api.Submitted](t, session, "submit_proposals", api.SubmitRequest{Session: "claude", Key: kind, Investigation: id, Alternatives: []inv.Alternative{a}})
		current := callTool[api.View](t, session, "get_investigation", api.GetRequest{Investigation: id})
		var proposal inv.Proposal
		for _, p := range current.Investigation.Proposals {
			if p.Group == submitted.Group {
				proposal = p
			}
		}
		r, e := operatorClient.Prepare(t.Context(), operator.Request{Investigation: id, Proposal: proposal.ID, Revision: proposal.Revision})
		require.NoError(t, e)
		require.Equal(t, kind, r.Action.Kind)
		require.Empty(t, r.Patch, "Executable bytes must not leave broker")
		outcome, e := operatorClient.Approve(t.Context(), approval(r))
		require.NoError(t, e)
		require.Equal(t, "accepted", outcome.State, "%s: %s", kind, outcome.Detail)
		cluster.mu.Lock()
		n := *cluster.deployment.Spec.Replicas
		cluster.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: cluster.deployment.Generation, Replicas: n, UpdatedReplicas: n, AvailableReplicas: n}
		cluster.mu.Unlock()
		require.Eventually(t, func() bool {
			v, e := client.Get(t.Context(), api.GetRequest{Investigation: id})
			return e == nil && v.Investigation.Executions[len(v.Investigation.Executions)-1].State == "completed"
		}, 3*time.Second, 20*time.Millisecond)
		current = callTool[api.View](t, session, "get_investigation", api.GetRequest{Investigation: id})
		require.Equal(t, "completed", current.Investigation.Executions[len(current.Investigation.Executions)-1].State)
	}
	cluster.mu.Lock()
	require.Equal(t, 4, cluster.writes)
	cluster.readDenied = true
	cluster.mu.Unlock()
	denied := callTool[api.Observation](t, session, "inspect_workload", api.InspectRequest{Investigation: id, Namespace: "shop", Name: "api"})
	require.Contains(t, strings.Join(denied.Evidence.Warnings, " "), "Permission denied")
	saved := callTool[api.HistoryResult](t, session, "query_history", api.HistoryRequest{Investigation: id, Namespace: "shop", Resource: "Deployment/api"})
	require.NotEmpty(t, saved.Records)
	for _, r := range saved.Records {
		require.Equal(t, started.Investigation.Cluster, r.Target.Cluster)
	}
	other := callTool[api.View](t, session, "start_investigation", api.StartRequest{Session: "other", Key: "start", Context: "explicit"})
	blocked, e := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_evidence", Arguments: api.EvidenceRequest{Investigation: other.Investigation.ID, Evidence: observed.Evidence.ID}})
	require.True(t, e != nil || blocked.IsError)
	// Linked outcomes are available to existing history consumers.
	historyResult, err := history.Query(o.History.Directory, history.Filter{Cluster: started.Investigation.Cluster})
	require.NoError(t, err)
	require.NotEmpty(t, historyResult.Records)
}
