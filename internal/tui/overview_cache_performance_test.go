package tui

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkOverviewRefresh(b *testing.B) {
	store := &overviewStore{dir: b.TempDir()}
	snapshot := overviewSnapshot{Key: "cluster", Apps: make([]overviewApp, 1000)}
	for i := range snapshot.Apps {
		snapshot.Apps[i] = overviewApp{Kind: "Deployment", Namespace: "shop", Name: fmt.Sprintf("api-%04d", i)}
	}
	snapshot.At = time.Now()
	store.save(snapshot)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		snapshot.At = time.Now()
		store.save(snapshot)
	}
}
