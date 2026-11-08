package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/broker/api"
	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/YunusAndreasson/safina/internal/investigation/store"
	"github.com/stretchr/testify/require"
)

func TestScopedEvidenceAndHistoryBounds(t *testing.T) {
	s, _, requests := scaleFixture(t)
	id := requests[0].Investigation
	s.opts.History.RedactPatterns = []string{"CUSTOM_PRIVATE"}
	e, err := s.capture(id, "shop", "api", "logs", strings.Repeat("password=supersecret CUSTOM_PRIVATE\n", 1000), false, nil)
	require.NoError(t, err)
	require.True(t, e.Evidence.Truncated)
	require.NotContains(t, e.Evidence.Text, "supersecret")
	require.NotContains(t, e.Evidence.Text, "CUSTOM_PRIVATE")
	current, err := s.store.Get(id)
	require.NoError(t, err)
	a := current.Proposals[0].Alternative
	a.Evidence = []string{e.Evidence.ID}
	_, err = s.store.Submit(id, "agent", "with-evidence", []inv.Alternative{a})
	require.NoError(t, err)
	other, err := s.store.Start(inv.Investigation{Session: "other", Cluster: "another-cluster", Context: "other"}, "start")
	require.NoError(t, err)
	_, err = s.evidence(api.EvidenceRequest{Investigation: other.ID, Evidence: e.Evidence.ID})
	require.ErrorIs(t, err, inv.ErrNotFound)
	a.Target.Cluster = other.Cluster
	_, err = s.store.Submit(other.ID, "other", "injected", []inv.Alternative{a})
	require.ErrorIs(t, err, inv.ErrInvalid)
	for _, cluster := range []string{current.Cluster, other.Cluster} {
		for range 70 {
			require.NoError(t, s.history.AppendDurable(history.Record{Version: 1, ID: inv.ID(), Time: time.Now().UTC(), Target: history.Target{Cluster: cluster, Namespace: "shop", Kind: "Deployment", Name: "api"}, Action: "scale", Outcome: "failed", Detail: strings.Repeat("CUSTOM_PRIVATE ", 40)}))
		}
	}
	result, err := s.historyQuery(api.HistoryRequest{Investigation: id, Namespace: "shop", Resource: "Deployment/api", Limit: 100})
	require.NoError(t, err)
	require.True(t, result.Truncated)
	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), 16<<10)
	require.NotContains(t, string(data), "CUSTOM_PRIVATE")
	for _, r := range result.Records {
		require.Equal(t, current.Cluster, r.Target.Cluster)
	}
	brief := s.historyBrief(current, &current.Proposals[0].Alternative.Target)
	require.LessOrEqual(t, len(brief), 16<<10)
	require.Contains(t, brief, "failed")
	require.NoError(t, s.store.Close())
	s.store, err = store.Open(s.opts.StateDir)
	require.NoError(t, err)
	defer func() { _ = s.store.Close() }()
	restored, err := s.evidence(api.EvidenceRequest{Investigation: id, Evidence: e.Evidence.ID})
	require.NoError(t, err)
	require.Equal(t, e.Evidence.Text, restored.Evidence.Text)
	saved, err := os.ReadFile(filepath.Join(s.opts.StateDir, "journal.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(saved), "supersecret")
	require.NotContains(t, string(saved), "CUSTOM_PRIVATE")
	_, err = s.evidence(api.EvidenceRequest{Investigation: id, Evidence: "../../etc/passwd"})
	require.ErrorIs(t, err, inv.ErrNotFound)
}

func TestHistoricalSecretEvidenceIsExcluded(t *testing.T) {
	s, _, requests := scaleFixture(t)
	id := requests[0].Investigation
	current, err := s.store.Get(id)
	require.NoError(t, err)
	evidenceID := inv.ID()
	require.NoError(t, s.history.AppendDurable(history.Record{Version: 1, ID: inv.ID(), Time: time.Now().UTC(), Target: history.Target{Cluster: current.Cluster, Kind: "Secret", Name: "credentials"}, Evidence: evidenceID, Detail: "unlabeled-secret-value", Parameters: map[string]string{"data": "unlabeled-secret-value"}}))
	out, err := s.historyQuery(api.HistoryRequest{Investigation: id})
	require.NoError(t, err)
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "unlabeled-secret-value")
	require.NotContains(t, string(b), evidenceID)
	_, err = s.evidence(api.EvidenceRequest{Investigation: id, Evidence: evidenceID})
	require.ErrorIs(t, err, inv.ErrInvalid)
}

func TestEvidenceCaptureOptOut(t *testing.T) {
	s, _, requests := scaleFixture(t)
	s.opts.History.Evidence = new(false)
	observed, err := s.capture(requests[0].Investigation, "shop", "api", "logs", "password=private", false, nil)
	require.NoError(t, err)
	require.Empty(t, observed.Evidence.ID)
	require.NotContains(t, observed.Evidence.Text, "private")
	require.Contains(t, strings.Join(observed.Evidence.Warnings, " "), "capture disabled")
	current, err := s.store.Get(requests[0].Investigation)
	require.NoError(t, err)
	require.Empty(t, current.Evidence)
}

func TestCurrentRedactionAppliesToReplayedEvidenceAndViews(t *testing.T) {
	s, _, requests := scaleFixture(t)
	id := requests[0].Investigation
	captured, err := s.capture(id, "shop", "api", "logs", "newly-sensitive-value", false, nil)
	require.NoError(t, err)
	s.opts.History.RedactPatterns = []string{"newly-sensitive-value"}
	returned, err := s.evidence(api.EvidenceRequest{Investigation: id, Evidence: captured.Evidence.ID})
	require.NoError(t, err)
	require.NotContains(t, returned.Evidence.Text, "newly-sensitive-value")
	current, err := s.store.Get(id)
	require.NoError(t, err)
	view := s.view(current)
	require.NotContains(t, view.Investigation.Evidence[0].Text, "newly-sensitive-value")
	// Display filtering must not mutate the authoritative evidence snapshot.
	current, err = s.store.Get(id)
	require.NoError(t, err)
	require.Equal(t, "newly-sensitive-value", current.Evidence[0].Text)
}

func TestBinaryLogEvidencePreservesUsefulSuffix(t *testing.T) {
	s, cluster, requests := scaleFixture(t)
	cluster.logText = "prefix\xff" + strings.Repeat("x", evidenceBytes) + "tail"
	observed, err := s.logs(t.Context(), api.LogsRequest{Investigation: requests[0].Investigation, Namespace: "shop", Pod: "api-123", Container: "web"})
	require.NoError(t, err)
	require.True(t, observed.Evidence.Truncated)
	require.Contains(t, observed.Evidence.Text, strings.Repeat("x", 100))
	require.LessOrEqual(t, len(observed.Evidence.Text), evidenceBytes)
}

func TestInterruptedLogStreamPreservesRedactedPartialEvidence(t *testing.T) {
	s, cluster, requests := scaleFixture(t)
	cluster.logText = "last useful line password=private\n"
	cluster.logInterrupted = true
	observed, err := s.logs(t.Context(), api.LogsRequest{Investigation: requests[0].Investigation, Namespace: "shop", Pod: "api-123", Container: "web"})
	require.NoError(t, err)
	require.True(t, observed.Evidence.Truncated)
	require.Contains(t, observed.Evidence.Text, "last useful line")
	require.NotContains(t, observed.Evidence.Text, "private")
	require.Contains(t, strings.Join(observed.Evidence.Warnings, " "), "coverage is incomplete")
	require.NotEmpty(t, observed.Evidence.ID)
	stored, err := s.evidence(api.EvidenceRequest{Investigation: requests[0].Investigation, Evidence: observed.Evidence.ID})
	require.NoError(t, err)
	require.Equal(t, observed.Evidence.Text, stored.Evidence.Text)
}

func TestBoundedTextNormalizesBinaryAndPreservesRuneBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text  string
		limit int
		want  string
		cut   bool
	}{
		{"a\xffb", 10, "a\uFFFDb", false},
		{"a\xffbc", 5, "a\uFFFDb", true},
		{"a船b", 3, "a", true},
		{"a船b", 4, "a船", true},
		{"plain", 0, "", true},
	} {
		got, cut := boundedText(tc.text, tc.limit)
		require.Equal(t, tc.want, got)
		require.Equal(t, tc.cut, cut)
	}
}
