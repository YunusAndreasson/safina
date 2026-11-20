package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(Options{Directory: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestJournalSurvivesRestartAndSeparatesOperations(t *testing.T) {
	s := testStore(t)
	a := s.Begin(Target{Cluster: "a", Context: "prod", Namespace: "shop", Kind: "Deployment", Name: "api", UID: "old"}, "scale", map[string]string{"replicas": "3"})
	b := s.Begin(Target{Cluster: "b", Context: "other", Namespace: "shop", Kind: "Deployment", Name: "api", UID: "new"}, "rollback", nil)
	b.Record("failed", "", nil)
	a.Record("accepted", "", nil)
	a.Record("completed", "3/3 ready", nil)
	require.NoError(t, s.Close())
	result, err := Query(s.Directory(), Filter{Cluster: "prod", Resource: "Deployment/api"})
	require.NoError(t, err)
	require.Len(t, result.Records, 3)
	for _, r := range result.Records {
		require.Equal(t, a.ID, r.Operation)
		require.Equal(t, types.UID("old"), r.Target.UID)
		require.Equal(t, 1, r.Version)
		require.False(t, r.Time.IsZero())
	}
	dest := filepath.Join(t.TempDir(), "incident")
	require.NoError(t, Export(s.Directory(), dest, Filter{Cluster: "prod"}))
	data, err := os.ReadFile(filepath.Join(dest, "events.jsonl")) // #nosec G304 -- Test-owned temporary export directory.
	require.NoError(t, err)
	require.Contains(t, string(data), "completed")
	require.NotContains(t, string(data), b.ID)
	require.Error(t, Export(s.Directory(), dest, Filter{}))
}

func TestConcurrentSessions(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	b, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Go(func() {
			for range 30 {
				s.Begin(Target{Cluster: "prod"}, "inspect", nil).Record("completed", "", nil)
			}
		})
	}
	wg.Wait()
	require.NoError(t, a.Close())
	require.NoError(t, b.Close())
	result, err := Query(dir, Filter{Limit: 200})
	require.NoError(t, err)
	require.Len(t, result.Records, 124)
	seen := map[string]bool{}
	for _, r := range result.Records {
		require.False(t, seen[r.ID])
		seen[r.ID] = true
	}
	limited, err := Query(dir, Filter{Limit: 5})
	require.NoError(t, err)
	require.Len(t, limited.Records, 5)
	require.Equal(t, 119, limited.Omitted)
}

func TestCaptureReassemblesAndRedactsBeforeDisk(t *testing.T) {
	s := testStore(t)
	op := s.Begin(Target{Cluster: "prod", Container: "api"}, "logs", nil)
	c := s.Capture(op)
	c.Append("2026-01-01T01:02:03Z token=sec")
	c.Append("ret hello\nplain line\n")
	c.Append(strings.Repeat("x", LineBytes+1))
	c.Append("\nlast")
	c.Close()
	data, err := ReadEvidence(s.Directory(), c.id)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
	require.Contains(t, string(data), "[REDACTED]")
	require.Contains(t, string(data), "oversized line omitted")
	var first EvidenceLine
	require.NoError(t, json.Unmarshal([]byte(strings.Split(string(data), "\n")[0]), &first))
	require.NotNil(t, first.Source)
	info, err := os.Stat(c.file.Name())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	r, err := Query(s.Directory(), Filter{ID: op.ID})
	require.NoError(t, err)
	require.Equal(t, "capture_truncated", r.Records[len(r.Records)-1].Outcome)
	_, err = ReadEvidence(s.Directory(), "../escape")
	require.Error(t, err)
}

func TestRetentionProtectsActiveWritersAndMarksMissingEvidence(t *testing.T) {
	s := testStore(t)
	op := s.Begin(Target{}, "logs", nil)
	c := s.Capture(op)
	c.Append("old\n")
	c.Close()
	old := time.Now().AddDate(0, 0, -15)
	require.NoError(t, os.Chtimes(c.file.Name(), old, old))
	require.NoError(t, s.Prune())
	_, err := ReadEvidence(s.Directory(), c.id)
	require.ErrorIs(t, err, os.ErrNotExist)
	dest := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, Export(s.Directory(), dest, Filter{}))
	b, err := os.ReadFile(filepath.Join(dest, "manifest.json")) // #nosec G304 -- Test-owned temporary export directory.
	require.NoError(t, err)
	require.Contains(t, string(b), "expired or unavailable")
}

func TestStorageFailureIsVisibleAndFailOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(dir, []byte("occupied"), 0o600))
	s, err := Open(Options{Directory: dir})
	require.Error(t, err)
	require.NotPanics(t, func() { s.Begin(Target{}, "delete", nil).Record("accepted", "", nil) })
	require.Contains(t, s.Health(), "Logbook incomplete")
	require.Error(t, s.Close())
}

func TestSummaryIsBoundedAndKeepsEvidenceUntrusted(t *testing.T) {
	s := testStore(t)
	for range 100 {
		s.Begin(Target{Cluster: "a", Name: "test"}, "restart", nil)
	}
	r, err := Query(s.Directory(), Filter{Limit: 50})
	require.NoError(t, err)
	text := Summary(r, 4096)
	require.Less(t, len(text), 4200)
	require.Contains(t, text, "untrusted evidence")
	require.Contains(t, text, "omitted")
}

func TestCapacityReservationsAndPrivateKeyRedaction(t *testing.T) {
	s, err := Open(Options{Directory: t.TempDir(), EvidenceBytes: CaptureBytes, ActionBytes: 4096})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	op := s.Begin(Target{}, "logs", nil)
	a := s.Capture(op)
	require.NotNil(t, a)
	require.Nil(t, s.Capture(op), "active capture reserves its maximum size")
	a.Append("-----BEGIN PRIVATE KEY-----\nprivate material\n-----END PRIVATE KEY-----\n")
	a.Close()
	data, err := ReadEvidence(s.Directory(), a.id)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private material")
	for range 40 {
		s.Begin(Target{}, "describe", nil)
	}
	var bytes int64
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT value FROM metadata WHERE key='bytes'").Scan(&bytes))
	require.LessOrEqual(t, bytes, int64(4096))
}

func TestCloseFlushesPendingCapture(t *testing.T) {
	s := testStore(t)
	c := s.Capture(s.Begin(Target{}, "logs", nil))
	c.Append("partial final line")
	require.NoError(t, s.Close())
	data, err := ReadEvidence(s.Directory(), c.id)
	require.NoError(t, err)
	require.Contains(t, string(data), "partial final line")
}

func TestReadEvidenceRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.jsonl")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../secret", "../../etc/passwd", "/etc/passwd", strings.Repeat("a", 31) + "/", ""} {
		if _, err := ReadEvidence(dir, id); err == nil {
			t.Errorf("ReadEvidence(%q) succeeded, want error", id)
		}
	}
	id := strings.Repeat("ab", 16)
	if err := os.WriteFile(filepath.Join(dir, "evidence", id+".jsonl"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadEvidence(dir, id); err != nil || string(b) != "ok" {
		t.Fatalf("valid ID: %q %v", b, err)
	}
}
