package bench

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// ScenarioID names one workload pattern. See plan §"Scenarios".
type ScenarioID string

const (
	S1ColdListPods    ScenarioID = "S1-cold-list-pods"
	S2RepeatListPods  ScenarioID = "S2-repeat-list-pods"
	S3ConnectionGraph ScenarioID = "S3-connection-graph"
	S4ClusterHealth   ScenarioID = "S4-cluster-health"
	S5ResourcesPerNS  ScenarioID = "S5-resources-per-ns"
)

func AllScenarios() []ScenarioID {
	return []ScenarioID{
		S1ColdListPods, S2RepeatListPods, S3ConnectionGraph,
		S4ClusterHealth, S5ResourcesPerNS,
	}
}

// Scenario is a callable that issues the API traffic for one iteration.
// It is variant-agnostic — it asks Options how to shape the calls.
//
// Context cancellation must abort in-flight goroutines; the runner uses a
// per-iteration deadline. The cs argument is the variant's clientset; opts
// carries the per-variant knobs (ListOptions, FanoutLimit, etc).
type Scenario struct {
	ID  ScenarioID
	Run func(ctx context.Context, cs kubernetes.Interface, opts *Options) error
}

func ScenarioRegistry() map[ScenarioID]Scenario {
	return map[ScenarioID]Scenario{
		S1ColdListPods:    {S1ColdListPods, runColdListPods},
		S2RepeatListPods:  {S2RepeatListPods, runRepeatListPods},
		S3ConnectionGraph: {S3ConnectionGraph, runConnectionGraph},
		S4ClusterHealth:   {S4ClusterHealth, runClusterHealth},
		S5ResourcesPerNS:  {S5ResourcesPerNS, runResourcesPerNS},
	}
}

func LookupScenario(id ScenarioID) (Scenario, error) {
	s, ok := ScenarioRegistry()[id]
	if !ok {
		return Scenario{}, fmt.Errorf("unknown scenario: %s", id)
	}
	return s, nil
}

// --- scenarios ---------------------------------------------------------------

// runColdListPods mirrors pods.go:94 — ListHotPodsByCPU's cluster-wide pod
// list. The hot-pods modal issues this on every open.
func runColdListPods(ctx context.Context, cs kubernetes.Interface, opts *Options) error {
	if opts.Informer != nil && opts.PodLister != nil {
		_, err := opts.PodLister.List(labels.Everything())
		return err
	}
	lo := opts.ListOptions
	if opts.UseFieldSelector {
		lo.FieldSelector = mergeFieldSelector(lo.FieldSelector, "status.phase=Running")
	}
	return listAllPods(ctx, cs, lo, opts.PaginatedContinue)
}

// runRepeatListPods mirrors the 5s hot-pods refresh tick — the same call
// fired 10 times in close succession. Surfaces the informer-cache win
// vs. raw List most clearly.
func runRepeatListPods(ctx context.Context, cs kubernetes.Interface, opts *Options) error {
	for range 10 {
		if err := runColdListPods(ctx, cs, opts); err != nil {
			return err
		}
	}
	return nil
}

// runConnectionGraph mirrors connections.go:507-573 — for a chosen Service,
// list Deployments/StatefulSets/DaemonSets/CronJobs/Jobs in its namespace.
// We use the "bench" namespace from the fixture seed.
func runConnectionGraph(ctx context.Context, cs kubernetes.Interface, opts *Options) error {
	const ns = "bench-0"
	lo := opts.ListOptions

	sem := makeSem(opts.FanoutLimit)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errOut error
	)
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if errOut == nil {
			errOut = err
		}
		mu.Unlock()
	}
	launch := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sem != nil {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			record(fn())
		}()
	}

	launch(func() error { _, err := cs.AppsV1().Deployments(ns).List(ctx, lo); return err })
	launch(func() error { _, err := cs.AppsV1().StatefulSets(ns).List(ctx, lo); return err })
	launch(func() error { _, err := cs.AppsV1().DaemonSets(ns).List(ctx, lo); return err })
	launch(func() error { _, err := cs.BatchV1().CronJobs(ns).List(ctx, lo); return err })
	launch(func() error { _, err := cs.BatchV1().Jobs(ns).List(ctx, lo); return err })

	wg.Wait()
	return errOut
}

// runClusterHealth mirrors tree.go:1252 — Nodes + Pods in parallel. (We
// skip the metrics-server call: minikube doesn't ship it by default, and
// LoadClusterHealth degrades gracefully on its absence.)
func runClusterHealth(ctx context.Context, cs kubernetes.Interface, opts *Options) error {
	lo := opts.ListOptions

	var (
		wg       sync.WaitGroup
		errNodes error
		errPods  error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errNodes = cs.CoreV1().Nodes().List(ctx, lo)
	}()
	go func() {
		defer wg.Done()
		if opts.Informer != nil && opts.PodLister != nil {
			_, errPods = opts.PodLister.List(labels.Everything())
			return
		}
		errPods = listAllPods(ctx, cs, lo, opts.PaginatedContinue)
	}()
	wg.Wait()
	if errNodes != nil {
		return errNodes
	}
	return errPods
}

// runResourcesPerNS mirrors resources.go fan-out — per-namespace ConfigMaps,
// Services, Secrets, Ingresses, PVCs across the bench-* namespaces.
func runResourcesPerNS(ctx context.Context, cs kubernetes.Interface, opts *Options) error {
	const nsCount = 10
	namespaces := make([]string, nsCount)
	for i := range nsCount {
		namespaces[i] = fmt.Sprintf("bench-%d", i)
	}
	lo := opts.ListOptions

	sem := makeSem(opts.FanoutLimit)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errOut error
	)
	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if errOut == nil {
			errOut = err
		}
		mu.Unlock()
	}
	launch := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sem != nil {
				sem <- struct{}{}
				defer func() { <-sem }()
			}
			record(fn())
		}()
	}

	for _, ns := range namespaces {
		launch(func() error { _, err := cs.CoreV1().ConfigMaps(ns).List(ctx, lo); return err })
		launch(func() error { _, err := cs.CoreV1().Services(ns).List(ctx, lo); return err })
		launch(func() error { _, err := cs.CoreV1().Secrets(ns).List(ctx, lo); return err })
		launch(func() error { _, err := cs.NetworkingV1().Ingresses(ns).List(ctx, lo); return err })
		launch(func() error {
			_, err := cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, lo)
			return err
		})
	}

	wg.Wait()
	return errOut
}

// --- helpers -----------------------------------------------------------------

// listAllPods drives the cluster-wide pod list with optional pagination. The
// returned data is discarded — we measure latency and bytes-on-wire, not
// what's in the response.
func listAllPods(ctx context.Context, cs kubernetes.Interface, lo metav1.ListOptions, paginate bool) error {
	if !paginate || lo.Limit == 0 {
		_, err := cs.CoreV1().Pods("").List(ctx, lo)
		return err
	}
	for {
		page, err := cs.CoreV1().Pods("").List(ctx, lo)
		if err != nil {
			return err
		}
		if page.Continue == "" {
			return nil
		}
		lo.Continue = page.Continue
	}
}

// Sentinel reference so the linter does not flag corev1 as unused — kept
// to make adding more typed paths cheap.
var _ = corev1.PodList{}

func makeSem(n int) chan struct{} {
	if n <= 0 {
		return nil
	}
	return make(chan struct{}, n)
}

func mergeFieldSelector(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "," + add
}
