package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*Store, string, inv.Investigation, inv.Alternative) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	current, err := s.Start(inv.Investigation{Session: "claude-a", Cluster: "cluster-a", Context: "production"}, "start")
	require.NoError(t, err)
	replicas := int32(3)
	a := inv.Alternative{Target: inv.Target{Cluster: current.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "payments", Name: "api", UID: "uid-a"}, Observed: inv.Observed{ResourceVersion: "12", Generation: 2, Time: time.Now().UTC()}, Action: inv.Action{Kind: "scale", Replicas: &replicas}}
	return s, dir, current, a
}

func TestConcurrentSubmissionsRecoveryAndSessions(t *testing.T) {
	s, dir, current, a := fixture(t)
	groups := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			group, err := s.Submit(current.ID, "claude-a", "same", []inv.Alternative{a})
			if err != nil {
				t.Error(err)
			}
			groups <- group
		})
	}
	wg.Wait()
	close(groups)
	first := <-groups
	for group := range groups {
		require.Equal(t, first, group)
	}
	other, err := s.Submit(current.ID, "claude-b", "same", []inv.Alternative{a})
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	changed := a
	changed.Rationale = "different"
	_, err = s.Submit(current.ID, "claude-a", "same", []inv.Alternative{changed})
	require.ErrorIs(t, err, inv.ErrConflict)
	// Cross-cluster batch is rejected atomically, even when its first item is valid.
	changed.Target.Cluster = "cluster-b"
	_, err = s.Submit(current.ID, "claude-a", "bad", []inv.Alternative{a, changed})
	require.ErrorIs(t, err, inv.ErrInvalid)
	*a.Action.Replicas = 999 // Caller cannot mutate persisted state.
	view, err := s.Get(current.ID)
	require.NoError(t, err)
	require.Len(t, view.Proposals, 2)
	require.EqualValues(t, 3, *view.Proposals[0].Alternative.Action.Replicas)
	require.NoError(t, s.Close())
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	view, err = reopened.Get(current.ID)
	require.NoError(t, err)
	require.Len(t, view.Proposals, 2)
	retried, err := reopened.Submit(current.ID, "claude-a", "same", []inv.Alternative{view.Proposals[0].Alternative})
	require.NoError(t, err)
	require.Equal(t, first, retried)
	otherStart, err := reopened.Start(inv.Investigation{Session: "claude-b", Cluster: "cluster-a", Context: "production"}, "start")
	require.NoError(t, err)
	require.NotEqual(t, current.ID, otherStart.ID)
}

func TestImmutableRevisionAndCompetingClaims(t *testing.T) {
	s, dir, current, a := fixture(t)
	_, err := s.Submit(current.ID, "claude-a", "group", []inv.Alternative{a, a})
	require.NoError(t, err)
	view, err := s.Get(current.ID)
	require.NoError(t, err)
	p := view.Proposals[0]
	edited := a
	edited.Rationale = "operator revision"
	next, err := s.Revise(current.ID, p.ID, 1, "operator", edited)
	require.NoError(t, err)
	require.Equal(t, 2, next.Revision)
	_, _, err = s.Select(current.ID, p.ID, 1, "review", "digest", "operator", nil)
	require.ErrorIs(t, err, inv.ErrConflict)
	var claims atomic.Int32
	var wg sync.WaitGroup
	for _, candidate := range []inv.Proposal{next, view.Proposals[1]} {
		wg.Go(func() {
			_, claimed, selectErr := s.Select(current.ID, candidate.ID, candidate.Revision, "review", "digest", "operator", []string{"hpa"})
			if selectErr != nil && !errors.Is(selectErr, inv.ErrConflict) {
				t.Error(selectErr)
			}
			if claimed {
				claims.Add(1)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, claims.Load())
	view, err = s.Get(current.ID)
	require.NoError(t, err)
	require.Len(t, view.Decisions, 1)
	require.Len(t, view.Executions, 1)
	require.Equal(t, a.Rationale, view.Proposals[0].Alternative.Rationale)
	decision := view.Decisions[0]
	_, claimed, err := s.Select(current.ID, decision.Proposal, decision.Revision, "review", "digest", "operator", nil)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, s.Close())
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	view, err = reopened.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", view.Executions[0].State)
	_, claimed, err = reopened.Select(current.ID, decision.Proposal, decision.Revision, "review", "digest", "operator", nil)
	require.NoError(t, err)
	require.False(t, claimed)
	require.ErrorIs(t, reopened.RecordOutcome(current.ID, inv.Execution{ID: view.Executions[0].ID, State: "completed"}), inv.ErrConflict)
}

func TestRecordingFailureNeverGrantsClaim(t *testing.T) {
	s, dir, current, a := fixture(t)
	_, err := s.Submit(current.ID, "claude-a", "group", []inv.Alternative{a})
	require.NoError(t, err)
	view, err := s.Get(current.ID)
	require.NoError(t, err)
	p := view.Proposals[0]
	s.syncFile = func() error { return errors.New("disk unavailable") }
	_, claimed, err := s.Select(current.ID, p.ID, 1, "review", "digest", "operator", nil)
	require.ErrorIs(t, err, inv.ErrRecording)
	require.False(t, claimed)
	_, err = s.Submit(current.ID, "claude-a", "new", []inv.Alternative{a})
	require.ErrorIs(t, err, inv.ErrRecording)
	require.NoError(t, s.Close())
	// A failed sync may still have written the intent. Recovery is conservative.
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	view, err = reopened.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "unknown", view.Executions[0].State)
	_, claimed, err = reopened.Select(current.ID, p.ID, 1, "review", "digest", "operator", nil)
	require.NoError(t, err)
	require.False(t, claimed)
}

func TestTailRecoveryAndExclusiveLock(t *testing.T) {
	s, dir, current, _ := fixture(t)
	_, err := Open(dir)
	require.Error(t, err)
	require.NoError(t, s.Close())
	f, err := os.OpenFile(filepath.Join(dir, "journal.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- Test-owned temporary journal.
	require.NoError(t, err)
	_, err = f.WriteString(`{"version":1,"partial":`)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	reopened, err := Open(dir)
	require.NoError(t, err)
	_, err = reopened.Get(current.ID)
	require.NoError(t, err)
	warnings, _ := reopened.Health()
	require.NotEmpty(t, warnings)
	tails, err := filepath.Glob(filepath.Join(dir, "*.tail"))
	require.NoError(t, err)
	require.Len(t, tails, 1)
	_, err = reopened.Start(inv.Investigation{Session: "new", Cluster: "cluster-a", Context: "a"}, "start")
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	again, err := Open(dir)
	require.NoError(t, err)
	require.NoError(t, again.Close())
}

func TestCompleteCorruptionBlocksOpen(t *testing.T) {
	for _, line := range []string{"not json\n", "{\"version\":99,\"eventId\":\"a\",\"sequence\":1,\"type\":\"started\",\"time\":\"2026-01-01T00:00:00Z\",\"payload\":{}}\n"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "journal.jsonl"), []byte(line), 0o600))
		_, err := Open(dir)
		require.Error(t, err)
		b, err := os.ReadFile(filepath.Join(dir, "journal.jsonl")) // #nosec G304 -- Test-owned temporary journal.
		require.NoError(t, err)
		require.Equal(t, line, string(b))
	}
}

func TestOutboxSurvivesFailedDeliveryAndAcknowledgement(t *testing.T) {
	s, dir, current, _ := fixture(t)
	require.Error(t, s.Deliver(func(history.Record) error { return errors.New("disk unavailable") }))
	_, pending := s.Health()
	require.Equal(t, 1, pending)
	journal, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { require.NoError(t, journal.Close()) }()
	s.syncFile = func() error { return errors.New("ack sync failed") }
	require.ErrorIs(t, s.Deliver(journal.AppendDurable), inv.ErrRecording)
	require.NoError(t, s.Close())
	// Simulate a lost acknowledgement by truncating just that final JSONL record.
	path := filepath.Join(dir, "journal.jsonl")
	b, err := os.ReadFile(path) // #nosec G304 -- Test-owned temporary journal.
	require.NoError(t, err)
	for i := len(b) - 2; i >= 0; i-- {
		if b[i] == '\n' {
			require.NoError(t, os.Truncate(path, int64(i+1)))
			break
		}
	}
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	require.NoError(t, reopened.Deliver(journal.AppendDurable))
	_, pending = reopened.Health()
	require.Zero(t, pending)
	result, err := history.Query(journal.Directory(), history.Filter{Cluster: current.Cluster})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, current.ID, result.Records[0].Investigation)
}

func TestRejectedAlternativeAndAcceptedOutcomeRecovery(t *testing.T) {
	s, dir, current, a := fixture(t)
	_, err := s.Submit(current.ID, "a", "batch", []inv.Alternative{a, a})
	require.NoError(t, err)
	view, err := s.Get(current.ID)
	require.NoError(t, err)
	first, second := view.Proposals[0], view.Proposals[1]
	require.NoError(t, s.Reject(current.ID, first.ID, 1, "operator"))
	_, _, err = s.Select(current.ID, first.ID, 1, "review", "digest", "operator", nil)
	require.ErrorIs(t, err, inv.ErrConflict)
	execution, claimed, err := s.Select(current.ID, second.ID, 1, "review", "digest", "operator", nil)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.RecordOutcome(current.ID, inv.Execution{ID: execution.ID, State: "accepted", UID: "uid", ResourceVersion: "2", Generation: 2}))
	require.NoError(t, s.Close())
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	view, err = reopened.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "accepted", view.Executions[0].State)
	require.NoError(t, reopened.RecordOutcome(current.ID, inv.Execution{ID: execution.ID, State: "completed", UID: "uid", ResourceVersion: "2", Generation: 2}))
	require.ErrorIs(t, reopened.RecordOutcome(current.ID, inv.Execution{ID: execution.ID, State: "intent"}), inv.ErrConflict)
}

func TestRedactionDoesNotCollapseIdempotencyConflicts(t *testing.T) {
	s, dir, current, a := fixture(t)
	a.Rationale = "password=first"
	_, err := s.Submit(current.ID, "claude", "key", []inv.Alternative{a})
	require.NoError(t, err)
	a.Rationale = "password=second"
	_, err = s.Submit(current.ID, "claude", "key", []inv.Alternative{a})
	require.ErrorIs(t, err, inv.ErrConflict)
	data, err := os.ReadFile(filepath.Join(dir, "journal.jsonl")) // #nosec G304 -- Test-owned temporary journal.
	require.NoError(t, err)
	require.NotContains(t, string(data), "password=first")
	require.NotContains(t, string(data), "password=second")
}

func TestOutcomeLinksItsDecisionAcrossMultipleGroups(t *testing.T) {
	s, _, current, a := fixture(t)
	executions := make([]inv.Execution, 0, 2)
	for _, key := range []string{"first", "second"} {
		_, err := s.Submit(current.ID, "a", key, []inv.Alternative{a})
		require.NoError(t, err)
		view, err := s.Get(current.ID)
		require.NoError(t, err)
		p := view.Proposals[len(view.Proposals)-1]
		execution, claimed, err := s.Select(current.ID, p.ID, 1, key, "digest", "operator", nil)
		require.NoError(t, err)
		require.True(t, claimed)
		executions = append(executions, execution)
	}
	first := executions[0]
	require.NoError(t, s.RecordOutcome(current.ID, inv.Execution{ID: first.ID, State: "accepted", UID: "uid", ResourceVersion: "2"}))
	var outcome history.Record
	require.NoError(t, s.Deliver(func(r history.Record) error {
		if r.Action == "investigation_outcome" {
			outcome = r
		}
		return nil
	}))
	require.Equal(t, first.ID, outcome.Operation)
	require.Equal(t, first.Decision, outcome.Decision)
	require.Equal(t, "accepted", outcome.Outcome)
}

func TestIndexOmitsHeavyHistoryAndActiveKeepsRecoveryWork(t *testing.T) {
	s, _, current, a := fixture(t)
	a.Rationale = strings.Repeat("large rationale", 10000)
	_, err := s.Submit(current.ID, "qa", "group", []inv.Alternative{a})
	require.NoError(t, err)
	_, err = s.Capture(current.ID, inv.Evidence{Text: strings.Repeat("e", 16000)})
	require.NoError(t, err)
	idle, err := s.Start(inv.Investigation{Session: "qa", Cluster: current.Cluster, Context: current.Context, Summary: strings.Repeat("s", 240000)}, "idle")
	require.NoError(t, err)
	index := s.Index()
	require.Len(t, index, 1)
	encoded, err := json.Marshal(index)
	require.NoError(t, err)
	require.Less(t, len(encoded), 2048)
	require.Empty(t, index[0].Evidence)
	require.Empty(t, index[0].Proposals[0].Alternative.Rationale)
	index[0].Proposals[0].State = "corrupted"
	full, err := s.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", full.Proposals[0].State)
	require.Equal(t, a.Rationale, full.Proposals[0].Alternative.Rationale)
	require.Len(t, full.Evidence, 1)
	require.Empty(t, s.Active())
	op, _, err := s.Select(current.ID, full.Proposals[0].ID, 1, "review", "digest", "operator", nil)
	require.NoError(t, err)
	require.Empty(t, s.Active()) // Intent is owned by the sender, not the monitor.
	op.State = "accepted"
	op.UID, op.ResourceVersion = "uid-a", "13"
	require.NoError(t, s.RecordOutcome(current.ID, op))
	active := s.Active()
	require.Len(t, active, 1)
	require.NotEqual(t, idle.ID, active[0].ID)
	active[0].Evidence[0].Text = "corrupted"
	full, err = s.Get(current.ID)
	require.NoError(t, err)
	require.NotEqual(t, "corrupted", full.Evidence[0].Text)
	op.State = "unknown"
	require.NoError(t, s.RecordOutcome(current.ID, op))
	require.Len(t, s.Active(), 1)
	op.State = "superseded"
	require.NoError(t, s.RecordOutcome(current.ID, op))
	require.Empty(t, s.Active())
}

func TestOversizedHistoryEventCannotPoisonOutbox(t *testing.T) {
	s, _, current, _ := fixture(t)
	journal, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { require.NoError(t, journal.Close()) }()
	require.NoError(t, s.Deliver(journal.AppendDurable))
	_, err = s.Start(inv.Investigation{Session: "qa", Cluster: current.Cluster, Context: strings.Repeat("x", history.LineBytes)}, "oversized")
	require.ErrorIs(t, err, inv.ErrInvalid)
	_, pending := s.Health()
	require.Zero(t, pending)
	_, err = s.Start(inv.Investigation{Session: "qa", Cluster: current.Cluster, Context: "normal"}, "normal")
	require.NoError(t, err)
	require.NoError(t, s.Deliver(journal.AppendDurable))
}

func TestSlowDeliveryDoesNotBlockStateReads(t *testing.T) {
	s, _, current, _ := fixture(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Deliver(func(history.Record) error { close(entered); <-release; return nil })
	}()
	<-entered
	read := make(chan error, 1)
	go func() { _, err := s.Get(current.ID); read <- err }()
	select {
	case err := <-read:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Error("logbook I/O blocks independent state reads")
	}
	close(release)
	require.NoError(t, <-done)
}

func TestRecoveryAtJournalSizeBoundary(t *testing.T) {
	makeJournal := func(dir, padding string) int64 {
		s, err := Open(dir)
		require.NoError(t, err)
		current := inv.Investigation{
			ID: "boundary", Session: "qa", Cluster: "cluster", Context: "test", Summary: padding,
			Executions: []inv.Execution{{ID: "operation", State: "intent"}},
		}
		require.NoError(t, s.commit("selected", &current, nil, "operation"))
		require.NoError(t, s.Close())
		info, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
		require.NoError(t, err)
		return info.Size()
	}
	size := makeJournal(t.TempDir(), "")
	dir := t.TempDir()
	makeJournal(dir, strings.Repeat("x", MaxRecordBytes-int(size)-40))
	s, err := Open(dir)
	require.NoError(t, err, "a valid persisted intent must remain recoverable")
	if s != nil {
		defer func() { require.NoError(t, s.Close()) }()
		current, err := s.Get("boundary")
		require.NoError(t, err)
		require.Equal(t, "unknown", current.Executions[0].State)
		op := current.Executions[0]
		op.Detail = "read-only reconciliation observed matching state; still unknown"
		require.NoError(t, s.RecordOutcome(current.ID, op))
		require.NoError(t, s.Close())
		s, err = Open(dir)
		require.NoError(t, err)
		replayed, err := s.Get(current.ID)
		require.NoError(t, err)
		require.Equal(t, current.Summary, replayed.Summary)
		require.Equal(t, op.Detail, replayed.Executions[0].Detail)
		info, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
		require.NoError(t, err)
		require.Less(t, info.Size(), int64(MaxRecordBytes+10000))
	}
}

func TestCompactOutcomeRejectsCorruptReferences(t *testing.T) {
	for _, change := range []string{"investigation", "claim", "decision", "state", "provenance"} {
		t.Run(change, func(t *testing.T) {
			s, dir, current, a := fixture(t)
			_, err := s.Submit(current.ID, "qa", "group", []inv.Alternative{a})
			require.NoError(t, err)
			current, err = s.Get(current.ID)
			require.NoError(t, err)
			op, _, err := s.Select(current.ID, current.Proposals[0].ID, 1, "review", "digest", "operator", nil)
			require.NoError(t, err)
			op.State, op.UID, op.ResourceVersion = "accepted", "uid", "version"
			require.NoError(t, s.RecordOutcome(current.ID, op))
			require.NoError(t, s.Close())
			path := filepath.Join(dir, "journal.jsonl")
			data, err := os.ReadFile(path) // #nosec G304 -- Test-owned temporary journal.
			require.NoError(t, err)
			lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
			var last event
			require.NoError(t, json.Unmarshal(lines[len(lines)-1], &last))
			u := last.Payload.ExecutionUpdate
			require.NotNil(t, u)
			switch change {
			case "investigation":
				u.Investigation = "missing"
			case "claim":
				u.Execution.Claim = "forged"
			case "decision":
				u.Execution.Decision = "forged"
			case "state":
				u.Execution.State = "completed"
			case "provenance":
				u.Execution.UID = ""
			}
			lines[len(lines)-1], err = json.Marshal(last)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600)) //nolint:gosec // Journal path inside this test's temporary directory.
			_, err = Open(dir)
			require.Error(t, err)
		})
	}
}

func TestDeliveryPreservesOrderAndAllowsConcurrentStateChanges(t *testing.T) {
	s, _, current, _ := fixture(t)
	second, err := s.Start(inv.Investigation{Session: "qa", Context: current.Context, Cluster: current.Cluster}, "second")
	require.NoError(t, err)
	var delivered []string
	require.NoError(t, s.Deliver(func(r history.Record) error {
		delivered = append(delivered, r.Investigation)
		r.Parameters["transition"] = "mutated callback copy"
		if len(delivered) == 1 {
			_, e := s.Start(inv.Investigation{Session: "qa", Context: current.Context, Cluster: current.Cluster}, "during-delivery")
			require.NoError(t, e)
		}
		return nil
	}))
	require.Equal(t, []string{current.ID, second.ID}, delivered)
	_, pending := s.Health()
	require.Equal(t, 1, pending)
	require.NoError(t, s.Deliver(func(r history.Record) error {
		require.Equal(t, "started", r.Parameters["transition"])
		return nil
	}))
}

func TestCapturedEvidenceDoesNotAliasCallerWarnings(t *testing.T) {
	s, _, current, _ := fixture(t)
	input := inv.Evidence{Text: "observed", Warnings: []string{"coverage gap"}}
	captured, err := s.Capture(current.ID, input)
	require.NoError(t, err)
	input.Warnings[0] = "caller changed input"
	got, err := s.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "coverage gap", got.Evidence[0].Warnings[0])
	captured.Warnings[0] = "caller changed result"
	got, err = s.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, "coverage gap", got.Evidence[0].Warnings[0])
}

func TestCaptureNormalizesBinaryBeforeTruncation(t *testing.T) {
	s, dir, current, _ := fixture(t)
	captured, err := s.Capture(current.ID, inv.Evidence{Text: "prefix\xff" + strings.Repeat("x", 17<<10)})
	require.NoError(t, err)
	require.True(t, captured.Truncated)
	require.Contains(t, captured.Text, "prefix\uFFFD"+strings.Repeat("x", 100))
	require.LessOrEqual(t, len(captured.Text), 16<<10)
	require.NoError(t, s.Close())
	reopened, err := Open(dir)
	require.NoError(t, err)
	defer func() { _ = reopened.Close() }()
	got, err := reopened.Get(current.ID)
	require.NoError(t, err)
	require.Equal(t, captured.Text, got.Evidence[0].Text)
}

func TestTornCompactAcceptanceRecoversWithoutNewClaim(t *testing.T) {
	for _, boundary := range []string{"first-byte", "middle", "missing-newline"} {
		t.Run(boundary, func(t *testing.T) {
			s, dir, current, a := fixture(t)
			_, err := s.Submit(current.ID, "qa", "group", []inv.Alternative{a})
			require.NoError(t, err)
			current, err = s.Get(current.ID)
			require.NoError(t, err)
			proposal := current.Proposals[0]
			op, claimed, err := s.Select(current.ID, proposal.ID, 1, "review", "digest", "operator", nil)
			require.NoError(t, err)
			require.True(t, claimed)
			op.State, op.UID, op.ResourceVersion = "accepted", "uid", "version"
			require.NoError(t, s.RecordOutcome(current.ID, op))
			require.NoError(t, s.Close())
			path := filepath.Join(dir, "journal.jsonl")
			data, err := os.ReadFile(path) // #nosec G304 -- Test-owned temporary journal.
			require.NoError(t, err)
			start := bytes.LastIndexByte(data[:len(data)-1], '\n') + 1
			tail := data[start:]
			keep := 1
			switch boundary {
			case "middle":
				keep = len(tail) / 2
			case "missing-newline":
				keep = len(tail) - 1
			}
			require.NoError(t, os.Truncate(path, int64(start+keep)))
			reopened, err := Open(dir)
			require.NoError(t, err)
			tails, err := filepath.Glob(filepath.Join(dir, "*.tail"))
			require.NoError(t, err)
			require.Len(t, tails, 1)
			preserved, err := os.ReadFile(tails[0]) // #nosec G304 -- Test-owned recovery tail.
			require.NoError(t, err)
			require.Equal(t, tail[:keep], preserved)
			recovered, err := reopened.Get(current.ID)
			require.NoError(t, err)
			require.Equal(t, "unknown", recovered.Executions[0].State)
			_, claimed, err = reopened.Select(current.ID, proposal.ID, 1, "review", "digest", "operator", nil)
			require.NoError(t, err)
			require.False(t, claimed)
			require.NoError(t, reopened.Close())
			again, err := Open(dir)
			require.NoError(t, err)
			defer func() { _ = again.Close() }()
			recovered, err = again.Get(current.ID)
			require.NoError(t, err)
			require.Equal(t, "unknown", recovered.Executions[0].State)
		})
	}
}
