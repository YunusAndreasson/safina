package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	inv "github.com/yunus/safina/internal/investigation"
)

func TestRetryRejectsUnencodableRequestWithoutReceipt(t *testing.T) {
	s := &Store{receipts: map[string]receipt{}}
	rc, found, err := s.retry("session", "start", "key", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	require.Error(t, err)
	require.False(t, found)
	require.Empty(t, rc)
	require.Empty(t, s.receipts)
}

func TestClonePreservesValuesAndDetachesMutableState(t *testing.T) {
	original := inv.Investigation{
		ID:        "investigation",
		Created:   time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		Target:    &inv.Target{Name: "target"},
		Evidence:  []inv.Evidence{{Warnings: []string{"warning"}}},
		Proposals: []inv.Proposal{{Alternative: inv.Alternative{Evidence: []string{"evidence"}, Action: inv.Action{Replicas: new(int32(2))}}}},
		Decisions: []inv.Decision{{Overrides: []string{"override"}, Prepared: &inv.Review{
			Action:   inv.Action{Replicas: new(int32(3))},
			Warnings: []string{"review warning"}, RequiredOverrides: []string{"required"},
		}}},
		Executions: []inv.Execution{{State: "unknown"}},
	}
	detached := clone(original)
	require.Equal(t, original, detached, "copying must not depend on JSON timestamp validity")
	detached.Target.Name = "changed"
	detached.Evidence[0].Warnings[0] = "changed"
	detached.Proposals[0].Alternative.Evidence[0] = "changed"
	*detached.Proposals[0].Alternative.Action.Replicas = 5
	detached.Decisions[0].Overrides[0] = "changed"
	detached.Decisions[0].Prepared.Warnings[0] = "changed"
	detached.Decisions[0].Prepared.RequiredOverrides[0] = "changed"
	*detached.Decisions[0].Prepared.Action.Replicas = 6
	detached.Executions[0].State = "changed"
	require.Equal(t, "target", original.Target.Name)
	require.Equal(t, "warning", original.Evidence[0].Warnings[0])
	require.Equal(t, "evidence", original.Proposals[0].Alternative.Evidence[0])
	require.Equal(t, int32(2), *original.Proposals[0].Alternative.Action.Replicas)
	require.Equal(t, "override", original.Decisions[0].Overrides[0])
	require.Equal(t, "review warning", original.Decisions[0].Prepared.Warnings[0])
	require.Equal(t, "required", original.Decisions[0].Prepared.RequiredOverrides[0])
	require.Equal(t, int32(3), *original.Decisions[0].Prepared.Action.Replicas)
	require.Equal(t, "unknown", original.Executions[0].State)
}
