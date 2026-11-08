package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

// Pending API commands are not run: local selection and paint must stay fast
// independently of cluster latency. Include full root routing, not only rows.
func benchmarkNavigationModel(b *testing.B, count int, pods, filtered bool) *Model {
	b.Helper()
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	b.Cleanup(m.ctx.Cancel)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m.ctx.ActiveContext = "demo"
	m.tree.PushContext("demo")
	m.tree.Push(sections.NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
	ch := &sections.Children{Kind: k8s.KindApp}
	for n := range count {
		name := fmt.Sprintf("api-%06d", count-n)
		if pods {
			ch.Kind = k8s.KindPod
			status := "Running"
			if n%7 == 0 {
				status = "CrashLoopBackOff"
			}
			ch.Pods = append(ch.Pods, k8s.PodInfo{Name: name, Namespace: "shop", Status: status, Age: "2d"})
		} else {
			ch.Apps = append(ch.Apps, k8s.AppInfo{Kind: k8s.AppKindDeployment, Name: name, Namespace: "shop", Health: k8s.HealthHealthy, Ready: "2/2", Age: "2d"})
		}
	}
	m.tree.SetChildren(m.tree.Path(), ch)
	if filtered {
		m.Update(keyMsg("/"))
		m.Update(tea.PasteMsg{Content: "api"})
		m.Update(keyMsg("enter"))
	}
	m.View()
	return &m
}

func BenchmarkNavigation(b *testing.B) {
	for _, pods := range []bool{false, true} {
		kind := "workloads"
		if pods {
			kind = "pods"
		}
		for _, count := range []int{100, 10000} {
			for _, filtered := range []bool{false, true} {
				for _, paint := range []bool{false, true} {
					b.Run(fmt.Sprintf("%s/%d/filter=%t/paint=%t", kind, count, filtered, paint), func(b *testing.B) {
						m := benchmarkNavigationModel(b, count, pods, filtered)
						down, up := keyMsg("down"), keyMsg("up")
						b.ReportAllocs()
						b.ResetTimer()
						for b.Loop() {
							m.Update(down)
							if paint {
								m.View()
							}
							m.Update(up)
							if paint {
								m.View()
							}
						}
					})
				}
			}
		}
	}
}
