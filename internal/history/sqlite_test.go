package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func legacyFixture(t *testing.T, dir string, r Record, tail string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "actions"), 0o700))
	b, err := json.Marshal(r)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "actions", "old.jsonl"), append(append(b, '\n'), tail...), 0o600))
}

func TestSQLiteMigrationRetainsIDsAndReportsTail(t *testing.T) {
	dir := t.TempDir()
	r := Record{Version: 1, ID: ID(), Session: "old", Sequence: 1, Time: time.Now().UTC(), Action: "scale", Outcome: "accepted", Target: Target{Cluster: "prod", Namespace: "shop", Kind: "Deployment", Name: "api"}, Operation: "op"}
	legacyFixture(t, dir, r, `{"broken":`)
	s, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	got, err := Query(dir, Filter{ID: r.ID})
	require.NoError(t, err)
	require.Equal(t, []Record{r}, got.Records)
	require.NotEmpty(t, got.Warnings)
	require.NoError(t, s.Close())
	s, err = Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	got, err = Query(dir, Filter{ID: "op", Namespace: "shop", Resource: "deployment/API"})
	require.NoError(t, err)
	require.Len(t, got.Records, 1)
	require.FileExists(t, filepath.Join(dir, "actions", "old.jsonl"))
	info, err := os.Stat(filepath.Join(dir, databaseName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSQLiteRetentionDoesNotReimportExpiredLegacy(t *testing.T) {
	dir := t.TempDir()
	r := Record{Version: 1, ID: ID(), Time: time.Now().AddDate(0, 0, -91).UTC(), Action: "old"}
	legacyFixture(t, dir, r, "")
	for range 2 {
		s, err := Open(Options{Directory: dir})
		require.NoError(t, err)
		got, err := Query(dir, Filter{ID: r.ID})
		require.NoError(t, err)
		require.Empty(t, got.Records)
		require.NotEmpty(t, got.Warnings)
		require.NoError(t, s.Close())
	}
}

func TestSQLiteMigrationCollisionRollsBack(t *testing.T) {
	dir := t.TempDir()
	r := Record{Version: 1, ID: ID(), Time: time.Now().UTC(), Action: "one"}
	legacyFixture(t, dir, r, "")
	r.Action = "two"
	b, err := json.Marshal(r)
	require.NoError(t, err)
	path := filepath.Join(dir, "actions", "second.jsonl")
	require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))
	s, err := Open(Options{Directory: dir})
	require.ErrorContains(t, err, "collision")
	_, queryErr := Query(dir, Filter{})
	require.ErrorContains(t, queryErr, "migration incomplete")
	_ = s.Close()
	require.NoError(t, os.Remove(path))
	s, err = Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	got, err := Query(dir, Filter{ID: r.ID})
	require.NoError(t, err)
	require.Len(t, got.Records, 1)
	require.Equal(t, "one", got.Records[0].Action)
}

func TestSQLiteConcurrentDurableDelivery(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = a.Close() }()
	b, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = b.Close() }()
	r := Record{Version: 1, ID: ID(), Time: time.Now().UTC(), Action: "selected"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []*Store{a, b} {
		wg.Go(func() { errs <- s.AppendDurable(r) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := Query(dir, Filter{ID: r.ID})
	require.NoError(t, err)
	require.Len(t, got.Records, 1)
}

func TestSQLiteReadOnlyQueryDoesNotCreateMissingStorage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	got, err := Query(dir, Filter{})
	require.NoError(t, err)
	require.Empty(t, got.Records)
	require.NoDirExists(t, dir)
}

func TestSQLiteFiltersMatchLegacy(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	records := []Record{
		{Version: 1, ID: "a", Session: "one", Sequence: 1, Time: now, Operation: "op", Target: Target{Cluster: "cluster", Context: "prod", Namespace: "shop", Kind: "Pod", Name: "api-123", WorkloadKind: "Deployment", WorkloadName: "api"}, CoverageGap: true},
		{Version: 1, ID: "b", Session: "one", Sequence: 2, Time: now, Operation: "op", Target: Target{Cluster: "cluster", Context: "prod", Namespace: "shop", Kind: "Pod", Name: "api-123"}},
		{Version: 1, ID: "c", Session: "two", Sequence: 1, Time: now.Add(time.Second), Target: Target{Cluster: "other", Namespace: "default", Kind: "Node", Name: "node"}},
	}
	legacyFixture(t, dir, records[0], "")
	f, err := os.OpenFile(filepath.Join(dir, "actions", "old.jsonl"), os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- Test-owned legacy fixture.
	require.NoError(t, err)
	for _, r := range records[1:] {
		require.NoError(t, json.NewEncoder(f).Encode(r))
	}
	require.NoError(t, f.Close())
	s, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	_, err = s.db.ExecContext(t.Context(), "DELETE FROM records WHERE session=?", s.session)
	require.NoError(t, err)
	for _, filter := range []Filter{
		{Until: now.Add(time.Second)},
		{Cluster: "prod"},
		{Cluster: "cluster", Namespace: "shop"},
		{Resource: "DEPLOYMENT/API"},
		{Resource: "api"},
		{Resource: "Pod/api-123"},
		{ID: "op"},
		{ID: "a"},
		{Session: "one"},
		{Since: now, Until: now},
		{Until: now.Add(time.Second), Limit: 1},
		{Cluster: "' OR 1=1 --"},
	} {
		// Exclude the fresh session marker, which exists only in SQLite.
		if filter.Until.IsZero() {
			filter.Until = now.Add(time.Nanosecond)
		}
		want, e := queryLegacy(dir, filter)
		require.NoError(t, e)
		got, e := Query(dir, filter)
		require.NoError(t, e)
		require.Equal(t, want, got, "filter: %+v", filter)
	}
}

func TestSQLiteRejectsDatabaseSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, databaseName)))
	_, err := Query(dir, Filter{})
	require.Error(t, err)
	s, err := Open(Options{Directory: dir})
	require.Error(t, err)
	_ = s.Close()
	data, err := os.ReadFile(target) // #nosec G304 -- Test-owned symlink target.
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data))
}

func TestSQLiteCapacityKeepsNewestRows(t *testing.T) {
	s, err := Open(Options{Directory: t.TempDir(), ActionBytes: 1024})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	var latest string
	for range 100 {
		latest = s.Append(Record{Action: "inspect", Detail: strings.Repeat("x", 200)})
		require.NotEmpty(t, latest)
	}
	got, err := Query(s.Directory(), Filter{})
	require.NoError(t, err)
	require.NotEmpty(t, got.Records)
	require.Less(t, len(got.Records), 100)
	require.Equal(t, latest, got.Records[len(got.Records)-1].ID)
	require.NotEmpty(t, got.Warnings)
	var size int64
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT SUM(bytes) FROM records").Scan(&size))
	require.LessOrEqual(t, size, int64(1024))
	var accounted int64
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT value FROM metadata WHERE key='bytes'").Scan(&accounted))
	require.Equal(t, size, accounted)
}

func TestSQLiteRefusesMigrationWithLegacyWriter(t *testing.T) {
	dir := t.TempDir()
	legacyFixture(t, dir, Record{Version: 1, ID: ID(), Time: time.Now().UTC()}, "")
	file, err := os.Open(filepath.Join(dir, "actions", "old.jsonl")) // #nosec G304 -- Test-owned legacy fixture.
	require.NoError(t, err)
	require.NoError(t, unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB))
	s, err := Open(Options{Directory: dir})
	require.ErrorContains(t, err, "close older Safina")
	_ = s.Close()
	require.NoError(t, file.Close())
	s, err = Open(Options{Directory: dir})
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestSQLiteWriteFailureRemainsVisibleAfterRecovery(t *testing.T) {
	s := testStore(t)
	_, err := s.db.ExecContext(t.Context(), "PRAGMA query_only=ON")
	require.NoError(t, err)
	require.Empty(t, s.Append(Record{Action: "lost"}))
	require.Contains(t, s.Health(), "Logbook incomplete")
	_, err = s.db.ExecContext(t.Context(), "PRAGMA query_only=OFF")
	require.NoError(t, err)
	id := s.Append(Record{Action: "recovered"})
	require.NotEmpty(t, id)
	got, err := Query(s.Directory(), Filter{ID: id})
	require.NoError(t, err)
	require.Len(t, got.Records, 1)
	require.True(t, got.Records[0].CoverageGap)
	require.NotEmpty(t, got.Warnings)
}
