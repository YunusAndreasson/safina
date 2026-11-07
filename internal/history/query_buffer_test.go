package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueryBoundedBufferMatchesFullQuery(t *testing.T) {
	dir := t.TempDir()
	actions := filepath.Join(dir, "actions")
	require.NoError(t, os.MkdirAll(actions, 0o700))
	for segment := range 3 {
		var data []byte
		for j := range 100 {
			i := ((segment*100 + j) * 37) % 300
			r := Record{Version: 1, ID: strconv.Itoa(i), Session: "session", Sequence: uint64(i), Time: time.Unix(int64(i%13), 0), Target: Target{Namespace: "keep"}, Parameters: map[string]string{"payload": strconv.Itoa(i)}}
			if i%5 == 0 {
				r.Target.Namespace = "other"
			}
			encoded, err := json.Marshal(r)
			require.NoError(t, err)
			data = append(data, encoded...)
			data = append(data, '\n')
		}
		data = append(data, "broken record\n"...)
		require.NoError(t, os.WriteFile(filepath.Join(actions, strconv.Itoa(segment)+".jsonl"), data, 0o600))
	}
	full, err := Query(dir, Filter{Namespace: "keep", Limit: 1000})
	require.NoError(t, err)
	require.Len(t, full.Records, 240)
	for _, limit := range []int{1, 7, 70, 239, 240, 241} {
		got, err := Query(dir, Filter{Namespace: "keep", Limit: limit})
		require.NoError(t, err)
		omitted := max(0, len(full.Records)-limit)
		require.Equal(t, full.Records[omitted:], got.Records)
		require.Equal(t, omitted, got.Omitted)
		require.Equal(t, full.Warnings, got.Warnings)
	}
}
