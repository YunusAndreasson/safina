package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func BenchmarkQueryHistory(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			dir := b.TempDir()
			actions := filepath.Join(dir, "actions")
			if err := os.MkdirAll(actions, 0o700); err != nil {
				b.Fatal(err)
			}
			file, err := os.Create(filepath.Join(actions, "records.jsonl")) // #nosec G304 -- Benchmark-owned temporary fixture.
			if err != nil {
				b.Fatal(err)
			}
			encoder := json.NewEncoder(file)
			for i := range count {
				record := Record{Version: 1, ID: strconv.Itoa(i), Time: time.Unix(int64(i), 0), Target: Target{Cluster: "prod", Namespace: "default", Kind: "Deployment", Name: "api"}, Action: "inspect", Outcome: "completed"}
				if err := encoder.Encode(record); err != nil {
					b.Fatal(err)
				}
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := Query(dir, Filter{Limit: 100})
				if err != nil {
					b.Fatal(err)
				}
				if len(result.Records) != 100 || result.Omitted != count-100 {
					b.Fatal("unexpected query result")
				}
			}
		})
	}
}
