package k8s

import (
	"fmt"
	"slices"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// BenchmarkAggregateForSelector models refresh-time enrichment when the pod
// snapshot includes other namespaces, as it does on multi-namespace views.
func BenchmarkAggregateForSelector(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			pods := make([]corev1.Pod, count)
			metrics := make(map[string]ResourceUsage, count)
			for i := range pods {
				pods[i].ObjectMeta = metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d", i), Namespace: fmt.Sprintf("ns-%d", i%100), Labels: map[string]string{"app": "api"}}
				metrics[pods[i].Namespace+"/"+pods[i].Name] = ResourceUsage{CPU: 10, Memory: 100}
			}
			selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				cpu, _ := aggregateForSelector("ns-0", selector, pods, metrics)
				if cpu != int64(count/100*10) {
					b.Fatal(cpu)
				}
			}
		})
	}
}

// Compare the old cluster-wide snapshot to the namespace-indexed snapshot,
// including snapshot allocations and ten workload aggregations per refresh.
func BenchmarkWorkloadPodSnapshot(b *testing.B) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	metrics := make(map[string]ResourceUsage, 10000)
	for i := range 10000 {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d", i), Namespace: fmt.Sprintf("ns-%d", i%100), Labels: map[string]string{"app": "api"}}}
		if err := indexer.Add(pod); err != nil {
			b.Fatal(err)
		}
		metrics[pod.Namespace+"/"+pod.Name] = ResourceUsage{CPU: 10, Memory: 100}
	}
	lister := corelisters.NewPodLister(indexer)
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
	for _, scoped := range []bool{false, true} {
		b.Run(map[bool]string{false: "cluster", true: "namespace"}[scoped], func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var pods []*corev1.Pod
				var err error
				if scoped {
					pods, err = lister.Pods("ns-0").List(labels.Everything())
				} else {
					pods, err = lister.List(labels.Everything())
				}
				if err != nil {
					b.Fatal(err)
				}
				snapshot := derefPods(pods)
				for range 10 {
					cpu, _ := aggregateForSelector("ns-0", selector, snapshot, metrics)
					if cpu != 1000 {
						b.Fatal(cpu)
					}
				}
			}
		})
	}
}

func BenchmarkBuildNodesAndPools(b *testing.B) {
	for _, count := range []int{100, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			nodes := make([]corev1.Node, 100)
			for i := range nodes {
				nodes[i].Name = fmt.Sprintf("node-%d", i)
			}
			pods := make([]corev1.Pod, count)
			for i := range pods {
				pods[i].Spec.NodeName = nodes[i%len(nodes)].Name
				pods[i].Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi"),
				}}}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, infos := buildNodesAndPools(nodes, nil, podPointers(pods))
				if len(infos) != len(nodes) {
					b.Fatal(len(infos))
				}
			}
		})
	}
}

// Include the conversion from informer pointers, not just the final aggregator.
func BenchmarkCachedPodProjection(b *testing.B) {
	nodes := make([]corev1.Node, 100)
	for i := range nodes {
		nodes[i].Name = fmt.Sprintf("node-%d", i)
	}
	pods := make([]*corev1.Pod, 10000)
	for i := range pods {
		pods[i] = &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d", i), Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{UID: types.UID(fmt.Sprintf("owner-%d", i%100))}}}, Spec: corev1.PodSpec{NodeName: nodes[i%100].Name}}
	}
	b.Run("nodes", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, infos := buildNodesAndPools(nodes, nil, slices.Values(pods))
			if len(infos) != 100 {
				b.Fatal(len(infos))
			}
		}
	})
	b.Run("owner", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			infos := filterPodsByOwner(slices.Values(pods), []types.UID{"owner-0"}, nil)
			if len(infos) != 100 {
				b.Fatal(len(infos))
			}
		}
	})
}

func BenchmarkPodsOnNodeIndex(b *testing.B) {
	informer := cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	typed := cache.NewTypedSharedIndexInformer[*corev1.Pod](informer)
	if err := typed.AddTypedIndexers(cache.TypedIndexers[*corev1.Pod]{podNodeIndex: podNodeIndexKeys}); err != nil {
		b.Fatal(err)
	}
	for i := range 10000 {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("pod-%d", i), Namespace: "ns"}, Spec: corev1.PodSpec{NodeName: fmt.Sprintf("node-%d", i%100)}}
		if err := informer.GetIndexer().Add(pod); err != nil {
			b.Fatal(err)
		}
	}
	lister := corelisters.NewPodLister(informer.GetIndexer())
	for _, indexed := range []bool{false, true} {
		b.Run(map[bool]string{false: "scan", true: "index"}[indexed], func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var pods []corev1.Pod
				if indexed {
					matches, err := typed.GetTypedIndexer().ByTypedIndex(podNodeIndex, "node-0")
					if err != nil {
						b.Fatal(err)
					}
					pods = derefPods(matches)
				} else {
					all, err := lister.List(labels.Everything())
					if err != nil {
						b.Fatal(err)
					}
					for _, p := range all {
						if p.Spec.NodeName == "node-0" {
							pods = append(pods, *p)
						}
					}
				}
				rows := podInfosSortedNSName(pods, nil)
				if len(rows) != 100 {
					b.Fatal(len(rows))
				}
			}
		})
	}
}
