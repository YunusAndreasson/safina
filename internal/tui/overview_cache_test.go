package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func overviewFixture(key string, at time.Time) overviewSnapshot {
	return snapshotOverview(key, at, &sections.Children{
		Kind:   k8s.KindNamespaceFolder,
		UserNS: []k8s.NamespaceInfo{{Name: "shop", Age: "2d"}}, SystemNS: []string{"kube-system"},
		Apps: []k8s.AppInfo{{Kind: "Deployment", Namespace: "shop", Name: "checkout", Health: k8s.HealthHealthy, CPU: 123, Memory: 456, Ready: "sensitive-extra-field"}},
	})
}

func TestOverviewDiskRoundTripAndProjection(t *testing.T) {
	dir := t.TempDir()
	store := &overviewStore{dir: dir}
	now := time.Now().Add(-time.Second).UTC()
	want := overviewFixture("cluster-a", now)
	store.save(want)
	// A new instance has no in-memory state, like a new application session.
	restarted := &overviewStore{dir: dir}
	got := restarted.load("cluster-a", time.Now())
	require.NotNil(t, got)
	require.Equal(t, want, *got)
	require.Nil(t, restarted.load("cluster-b", time.Now()))
	ch := got.children()
	require.Equal(t, k8s.KindNamespaceFolder, ch.Kind)
	require.Equal(t, 1, ch.SystemCount)
	require.Zero(t, ch.Apps[0].CPU)
	require.Zero(t, ch.Apps[0].Memory)
	require.Empty(t, ch.Apps[0].Ready)
	data, err := os.ReadFile(filepath.Join(dir, "v1.json")) // #nosec G304 -- Fixed cache filename beneath t.TempDir().
	require.NoError(t, err)
	require.NotContains(t, string(data), "sensitive-extra-field")
	require.NotContains(t, string(data), "CPU")
	info, err := os.Stat(filepath.Join(dir, "v1.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	// Returned tree slices cannot mutate the persistent/memory projection.
	ch.UserNS[0].Name = "changed"
	ch.Apps[0].Name = "changed"
	require.Equal(t, "shop", restarted.load("cluster-a", time.Now()).Namespaces[0].Name)
	require.Equal(t, "checkout", restarted.load("cluster-a", time.Now()).Apps[0].Name)
}

func TestOverviewLimitsExpiryAndVersion(t *testing.T) {
	dir := t.TempDir()
	store := &overviewStore{dir: dir}
	now := time.Now().Add(-time.Minute)
	for i := range overviewMaxEntries + 2 {
		store.save(overviewFixture(strconv.Itoa(i), now.Add(time.Duration(i)*time.Second)))
	}
	require.Len(t, store.memory, overviewMaxEntries)
	require.Len(t, store.readFile(), overviewMaxEntries)
	require.Nil(t, (&overviewStore{dir: dir}).load("0", time.Now()))
	require.NotNil(t, (&overviewStore{dir: dir}).load("5", time.Now()))
	require.Nil(t, store.load("5", now.Add(overviewTTL+time.Hour)))
	require.Nil(t, store.load("5", now.Add(-time.Hour)), "future snapshots must not be treated as fresh")
	oversized := overviewFixture("oversized", time.Now())
	oversized.Apps[0].Name = strings.Repeat("x", overviewMaxBytes)
	store.save(oversized)
	require.Nil(t, store.load("oversized", time.Now()))
	for _, data := range [][]byte{[]byte("broken"), []byte(strings.Repeat("x", overviewMaxBytes+1)), []byte(`{"Version":999,"Entries":[]}`)} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.json"), data, 0o600))
		require.Nil(t, (&overviewStore{dir: dir}).load("5", time.Now()))
	}
}

func TestOverviewOlderWriteCannotReplaceNewerSnapshot(t *testing.T) {
	store := &overviewStore{dir: t.TempDir()}
	now := time.Now().Add(-time.Second)
	newer := overviewFixture("cluster", now)
	older := overviewFixture("cluster", now.Add(-time.Minute))
	older.Apps[0].Name = "old"
	store.save(newer)
	store.save(older)
	got := (&overviewStore{dir: store.dir}).load("cluster", time.Now())
	require.NotNil(t, got)
	require.Equal(t, "checkout", got.Apps[0].Name)
	require.Equal(t, "checkout", store.load("cluster", time.Now()).Apps[0].Name)
	// Invalid versions never enter the reusable in-memory cache.
	file := overviewFile{Version: overviewVersion + 1, Entries: []overviewSnapshot{newer}}
	data, err := json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store.dir, "v1.json"), data, 0o600))
	require.Nil(t, (&overviewStore{dir: store.dir}).load("cluster", time.Now()))
}

func TestOverviewRefreshKeepsMemoryFreshAndBoundsDiskWrites(t *testing.T) {
	store := &overviewStore{dir: t.TempDir()}
	now := time.Now().UTC()
	first := overviewFixture("cluster", now)
	store.saveAt(first, now)
	latest := overviewFixture("cluster", now.Add(5*time.Second))
	latest.Apps[0].Name = "new-workload"
	store.saveAt(latest, latest.At)
	require.Equal(t, "new-workload", store.load("cluster", latest.At).Apps[0].Name)
	require.Equal(t, "checkout", (&overviewStore{dir: store.dir}).load("cluster", latest.At).Apps[0].Name)
	// A different cluster must get its first persistent preview immediately.
	other := overviewFixture("other", latest.At)
	store.saveAt(other, latest.At)
	require.NotNil(t, (&overviewStore{dir: store.dir}).load("other", latest.At))
	latest.At = now.Add(overviewWriteInterval)
	store.saveAt(latest, latest.At)
	require.Equal(t, "new-workload", (&overviewStore{dir: store.dir}).load("cluster", latest.At).Apps[0].Name)
	for i := range 10 {
		store.saveAt(overviewFixture(strconv.Itoa(i), latest.At), latest.At)
	}
	require.LessOrEqual(t, len(store.diskWritten), overviewMaxEntries)
}

func TestOverviewFailedWriteRemainsRetryable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.WriteFile(dir, []byte("blocks directory creation"), 0o600))
	store := &overviewStore{dir: dir}
	now := time.Now().UTC()
	snapshot := overviewFixture("cluster", now)
	store.saveAt(snapshot, now)
	require.Empty(t, store.diskWritten)
	require.NotNil(t, store.load("cluster", now), "disk failure must not disable memory previews")
	require.NoError(t, os.Remove(dir))
	store.saveAt(snapshot, now.Add(time.Second))
	require.NotNil(t, (&overviewStore{dir: dir}).load("cluster", now.Add(time.Second)))
}

func TestOverviewConcurrentRefreshKeepsNewestMemorySnapshot(t *testing.T) {
	store := &overviewStore{dir: t.TempDir()}
	now := time.Now().UTC()
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			snapshot := overviewFixture("cluster", now.Add(time.Duration(i)*time.Nanosecond))
			snapshot.Apps[0].Name = strconv.Itoa(i)
			store.saveAt(snapshot, now.Add(time.Second))
			store.load("cluster", now.Add(time.Second))
		})
	}
	wg.Wait()
	require.Equal(t, "31", store.load("cluster", now.Add(time.Second)).Apps[0].Name)
	require.Len(t, store.readFile(), 1, "concurrent writers must leave a complete JSON file")
}
