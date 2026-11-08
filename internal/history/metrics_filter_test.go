package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMetricActionFilterBeforeLimit(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "actions"), 0o700))
	var data []byte
	now := time.Now().UTC()
	for i, r := range []Record{
		{ID: "matching", Action: "restart", Target: Target{UID: "wanted"}},
		{ID: "child", Action: "evict", Target: Target{UID: "pod", WorkloadUID: "wanted"}},
		{ID: "browse", Action: "browse", Target: Target{UID: "wanted"}},
		{ID: "recreated", Action: "restart", Target: Target{UID: "other"}},
	} {
		r.Version = 1
		r.Time = now.Add(time.Duration(i) * time.Second)
		b, err := json.Marshal(r)
		require.NoError(t, err)
		data = append(data, append(b, '\n')...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "actions", "old.jsonl"), data, 0o600))
	filter := Filter{UID: "wanted", Actions: []string{"restart", "evict"}, Limit: 1}
	legacy, err := Query(dir, filter)
	require.NoError(t, err)
	require.Len(t, legacy.Records, 1)
	require.Equal(t, "child", legacy.Records[0].ID)
	require.Equal(t, 1, legacy.Omitted)
	s, err := Open(Options{Directory: dir})
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	sqlite, err := Query(dir, filter)
	require.NoError(t, err)
	require.Equal(t, legacy.Records, sqlite.Records)
	require.Equal(t, legacy.Omitted, sqlite.Omitted)
}
