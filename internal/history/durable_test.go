package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDurableDeliveryDeduplicatesAndExportsLinkage(t *testing.T) {
	s, err := Open(Options{Directory: t.TempDir()})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	record := Record{Version: 1, ID: ID(), Session: "broker", Time: time.Now().UTC(), Target: Target{Cluster: "cluster"}, Investigation: "investigation", Proposal: "proposal", Revision: 2, Decision: "decision", Action: "selected", Outcome: "recorded", Detail: "token=private"}
	require.NoError(t, s.AppendDurable(record))
	require.NoError(t, s.AppendDurable(record))
	result, err := Query(s.Directory(), Filter{Cluster: "cluster"})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, record.ID, result.Records[0].ID)
	require.NotContains(t, result.Records[0].Detail, "private")
	record.Revision++
	require.Error(t, s.AppendDurable(record))
	require.NoError(t, Export(s.Directory(), filepath.Join(t.TempDir(), "export"), Filter{Cluster: "cluster"}))
}

func TestDurableDeliveryFailsWhenDisabled(t *testing.T) {
	enabled := false
	s, err := Open(Options{Directory: t.TempDir(), Enabled: &enabled})
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	require.Error(t, s.AppendDurable(Record{Version: 1, ID: ID(), Time: time.Now().UTC()}))
}

func TestIndependentRedactorValidatesCustomPatterns(t *testing.T) {
	redact, err := NewRedactor([]string{`customer-[0-9]+`})
	require.NoError(t, err)
	require.Equal(t, "[REDACTED] token=[REDACTED]", redact("customer-123 token=secret"))
	_, err = NewRedactor([]string{"["})
	require.Error(t, err)
}
