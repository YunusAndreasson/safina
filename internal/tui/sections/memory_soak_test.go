package sections

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
)

// Explicitly enabled: a long-session heap check without network calls or writes
// to operator preferences. Retain the same owners throughout the workload.
func TestMemoryRetentionSoak(t *testing.T) {
	requested := os.Getenv("SAFINA_MEMORY_SOAK_DURATION")
	if requested == "" {
		t.Skip("set SAFINA_MEMORY_SOAK_DURATION to run the retained-heap soak")
	}
	duration, err := time.ParseDuration(requested)
	require.NoError(t, err)
	require.Positive(t, duration)
	tr := newTestTree(t)
	tr.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tr.SetChildren(tr.Path(), &Children{Kind: k8s.KindApp})
	i, c := NewInspector(), NewConnected()
	l := retentionLogs()
	payload := strings.Repeat("INFO a bounded stream record with enough text to exercise retention\n", 16000)
	run := func(cycle int) {
		for n := range 512 {
			name := fmt.Sprintf("cycle-%d-pod-%d", cycle, n)
			tr.Push(NodeRef{Kind: k8s.KindApp, Name: name})
			tr.SetChildren(tr.Path(), &Children{Kind: k8s.KindPod, Pods: []k8s.PodInfo{{Name: name, Namespace: "shop"}}})
			tr.Pop()
			i.Apply(InspectorDetailMsg{Kind: k8s.KindPod, Namespace: "shop", Name: name}, nil)
			c.Apply(ConnectedMsg{Kind: k8s.KindPod, Namespace: "shop", Name: name})
		}
		for range 10 {
			l.Append(payload)
		}
	}
	for cycle := range 5 {
		run(cycle)
	}
	runtime.GC()
	var baseline, latest runtime.MemStats
	runtime.ReadMemStats(&baseline)
	deadline := time.Now().Add(duration)
	cycles := 0
	for time.Now().Before(deadline) {
		run(cycles + 5)
		cycles++
		runtime.GC()
		runtime.ReadMemStats(&latest)
		require.LessOrEqual(t, len(tr.cache), maxTreeCacheEntries)
		require.LessOrEqual(t, len(tr.cursorByKey), maxNavigationPositions)
		require.LessOrEqual(t, len(i.detail), maxDetailCacheEntries)
		require.LessOrEqual(t, len(c.cache), maxDetailCacheEntries)
		require.LessOrEqual(t, len(l.lines), maxLogLines)
		require.LessOrEqual(t, l.logBytes, maxLogBytes)
		if cycles%10 == 0 {
			t.Logf("cycles=%d heap=%d KiB goroutines=%d", cycles, latest.HeapAlloc/1024, runtime.NumGoroutine())
		}
	}
	growth := uint64(0)
	if latest.HeapAlloc > baseline.HeapAlloc {
		growth = latest.HeapAlloc - baseline.HeapAlloc
	}
	t.Logf("completed %d measured cycles; baseline=%d KiB final=%d KiB growth=%d KiB", cycles, baseline.HeapAlloc/1024, latest.HeapAlloc/1024, growth/1024)
	runtime.KeepAlive(&tr)
	runtime.KeepAlive(&i)
	runtime.KeepAlive(&c)
	runtime.KeepAlive(&l)
	require.Less(t, growth, uint64(8<<20), "live heap must plateau while the same owners remain alive")
}
