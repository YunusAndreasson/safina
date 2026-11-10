package sections

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/metrics"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

func cachePods(n int) []k8s.PodInfo {
	statuses := []string{"Running", "Pending", "CrashLoopBackOff", "Running", "Completed"}
	pods := make([]k8s.PodInfo, n)
	for i := range pods {
		pods[i] = k8s.PodInfo{
			Name:      fmt.Sprintf("api-%03d-7f77847d67-%05d", i, i),
			Namespace: fmt.Sprintf("shop-%d", i%3),
			Status:    statuses[i%len(statuses)],
			Restarts:  int32(i % 4),
			Age:       "2d",
			CPU:       int64(10 + i),
			Memory:    int64(i+1) << 20,
		}
	}
	return pods
}

// A frame drawn from cached rows must be byte-identical to drawing every row
// again, whatever changed in between: the cursor, focus, a pod's data, its
// metrics, the width, or staleness. Without metrics columns nothing else in
// the key carries the row background, so both layouts are checked.
func TestPodRowCacheMatchesFreshRender(t *testing.T) {
	for _, withMetrics := range []bool{true, false} {
		t.Run(fmt.Sprintf("metrics=%v", withMetrics), func(t *testing.T) { checkPodRowCache(t, withMetrics) })
	}
}

func checkPodRowCache(t *testing.T, withMetrics bool) {
	t.Helper()
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	cachedTree, freshTree := NewTree(c), NewTree(c)
	cached, fresh := &cachedTree, &freshTree
	trees := []*Tree{cached, fresh}
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	push := func(cpu int64) {
		if !withMetrics {
			return
		}
		for _, tr := range trees {
			for _, p := range tr.children.Pods {
				tr.pushMetricReading(k8s.KindPod, p.Namespace, p.Name, "", metrics.Reading{CPU: cpu + p.CPU, Memory: p.Memory, State: metrics.Available, SourceAt: now, Covered: 1, Expected: 1}, now)
			}
		}
		now = now.Add(time.Minute)
	}
	pods := func(n int) []k8s.PodInfo {
		ps := cachePods(n)
		if !withMetrics {
			for i := range ps {
				ps[i].CPU, ps[i].Memory = 0, 0
			}
		}
		return ps
	}
	for _, tr := range trees {
		tr.children = &Children{Kind: k8s.KindPod, Pods: pods(80)}
		tr.Focused = true
		tr.alphaSort = true // data order: a status change stays where it is drawn
	}
	push(0)
	push(5)

	w, h := 140, 30
	check := func(step string) {
		t.Helper()
		fresh.podRows = rowCache{}
		want := fresh.View(c, w, h)
		require.Equal(t, want, cached.View(c, w, h), step)
	}
	each := func(fn func(*Tree)) {
		for _, tr := range trees {
			fn(tr)
		}
	}

	check("first frame")
	check("unchanged second frame")
	for i := 1; i <= 40; i++ { // past the bottom: the window scrolls
		each(func(tr *Tree) { tr.cursor = i })
		check(fmt.Sprintf("cursor %d", i))
	}
	each(func(tr *Tree) { tr.children.Pods[40].Status = "Failed" })
	check("selected pod changed status")
	each(func(tr *Tree) { tr.children.Pods[35].Status = "Running"; tr.children.Pods[36].Status = "Pending" })
	check("visible pods changed status")
	each(func(tr *Tree) {
		tr.children = &Children{Kind: k8s.KindPod, Pods: append(pods(1), tr.children.Pods...)}
		tr.children.Pods[0].Name = "aaa-first"
	})
	check("a new pod shifts every row's stripe")
	each(func(tr *Tree) { tr.alphaSort = false })
	check("health order")
	each(func(tr *Tree) { tr.children.Pods[30].Age = "3d"; tr.children.Pods[31].Restarts = 9 })
	check("visible pods changed age and restarts")
	push(50)
	check("new metric sample")
	each(func(tr *Tree) { tr.Focused = false })
	check("tree lost focus")
	w = 100
	check("narrower")
	c.Stale = true
	check("stale metrics")
	c.Stale = false
	each(func(tr *Tree) { tr.children = &Children{Kind: k8s.KindPod, Pods: pods(80)[:5]}; tr.cursor = 0 })
	check("fewer pods")
	each(func(tr *Tree) { // one namespace drops the NS column
		ps := pods(6)
		for i := range ps {
			ps[i].Namespace = "shop-0"
		}
		tr.children = &Children{Kind: k8s.KindPod, Pods: ps}
	})
	check("single namespace")
}

// Only the rows a frame draws stay cached.
func TestRowCacheKeepsOnlyTheLastFrame(t *testing.T) {
	th := theme.Default()
	var cache rowCache
	renders := 0
	row := func(key string) string {
		return cache.get(th, key, func() string { renders++; return "row " + key })
	}
	row("a")
	row("b")
	cache.endFrame()
	require.Equal(t, "row a", row("a"))
	require.Equal(t, 2, renders, "a was reused")
	cache.endFrame() // b was not drawn this frame
	row("b")
	require.Equal(t, 3, renders, "b was dropped and rendered again")
	cache.endFrame()
	row("b")
	require.Equal(t, 3, renders)
	cache.get(theme.Default(), "b", func() string { renders++; return "" })
	require.Equal(t, 4, renders, "another theme starts over")
}

// A cursor move on a 500-pod list at 200×50, drawn with and without reused rows.
func BenchmarkTreePodCursor(b *testing.B) {
	for _, reuse := range []bool{true, false} {
		b.Run(fmt.Sprintf("cache=%v", reuse), func(b *testing.B) {
			c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
			tree := NewTree(c)
			tree.children = &Children{Kind: k8s.KindPod, Pods: cachePods(500)}
			tree.Focused = true
			tree.View(c, 200, 50)
			b.ReportAllocs()
			b.ResetTimer()
			i := 0
			for b.Loop() {
				i = (i + 1) % 40
				tree.cursor = i
				if !reuse {
					tree.podRows = rowCache{}
				}
				tree.View(c, 200, 50)
			}
		})
	}
}
