package sections

import (
	"fmt"
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

func BenchmarkTreePodView(b *testing.B) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, count := range []int{100, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			tree := NewTree(c)
			tree.children = &Children{Kind: k8s.KindPod, Pods: make([]k8s.PodInfo, count)}
			for i := range tree.children.Pods {
				tree.children.Pods[i] = k8s.PodInfo{Name: fmt.Sprintf("api-%06d", count-i), Namespace: "default", Status: "Running", Age: "2d"}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tree.View(c, 100, 30)
			}
		})
	}
}

func BenchmarkTreeAppView(b *testing.B) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, query := range []string{"", "api", "api-000001", "missing"} {
		b.Run("query="+query, func(b *testing.B) {
			tree := NewTree(c)
			tree.alphaSort = true
			tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold})
			tree.children = &Children{Kind: k8s.KindApp, Apps: make([]k8s.AppInfo, 10000)}
			for i := range tree.children.Apps {
				tree.children.Apps[i] = k8s.AppInfo{Name: fmt.Sprintf("api-%06d", 10000-i), Namespace: "default", Kind: "Deployment", Health: k8s.HealthHealthy, Ready: "1/1", Age: "2d"}
			}
			tree.filter.Open()
			tree.filter.Update(tea.PasteMsg{Content: query})
			tree.filter.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tree.View(c, 100, 30)
			}
		})
	}
}

func BenchmarkTreeFilteredPodView(b *testing.B) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, query := range []string{"api", "api-000001", "missing"} {
		b.Run(query, func(b *testing.B) {
			tree := NewTree(c)
			tree.children = &Children{Kind: k8s.KindPod, Pods: make([]k8s.PodInfo, 10000)}
			for i := range tree.children.Pods {
				status := "Running"
				if i%7 == 0 {
					status = "CrashLoopBackOff"
				}
				tree.children.Pods[i] = k8s.PodInfo{Name: fmt.Sprintf("api-%06d", 10000-i), Namespace: "default", Status: status, Age: "2d"}
			}
			tree.filter.Open()
			tree.filter.Update(tea.PasteMsg{Content: query})
			tree.filter.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tree.View(c, 100, 30)
			}
		})
	}
}

func BenchmarkTreeSelectedRef(b *testing.B) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, kind := range []string{k8s.KindApp, k8s.KindPod} {
		b.Run(kind, func(b *testing.B) {
			tree := NewTree(c)
			tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold})
			tree.children = &Children{Kind: kind, Apps: make([]k8s.AppInfo, 10000), Pods: make([]k8s.PodInfo, 10000)}
			for i := range tree.children.Apps {
				tree.children.Apps[i] = k8s.AppInfo{Name: fmt.Sprintf("api-%06d", i)}
				tree.children.Pods[i] = k8s.PodInfo{Name: fmt.Sprintf("api-%06d", i), Status: "Running"}
			}
			tree.cursor = 5000
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tree.SelectedRef()
			}
		})
	}
}
