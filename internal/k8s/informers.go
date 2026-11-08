package k8s

import (
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	autoscalingv2listers "k8s.io/client-go/listers/autoscaling/v2"
	batchv1listers "k8s.io/client-go/listers/batch/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	networkingv1listers "k8s.io/client-go/listers/networking/v1"
	toolscache "k8s.io/client-go/tools/cache"

	"github.com/YunusAndreasson/safina/internal/debug"
)

// lastAppliedAnnotation is stripped from every cached object — it duplicates
// the whole spec as a JSON blob and safina never reads it.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// kindCache pairs one kind's lister with its "initial sync landed" flag.
// The flag is flipped asynchronously by a per-informer WaitForCacheSync
// goroutine, so reads go through atomics — tea.Cmd goroutines consult these
// listers while the sync goroutines are still running.
type kindCache[L any] struct {
	lister L
	synced atomic.Bool
	start  func()
	once   sync.Once
}

func (k *kindCache[L]) ready() bool {
	if k.start != nil {
		k.once.Do(k.start)
	}
	return k.synced.Load()
}

// informerSet owns the shared informer factory and one lister per kind the
// tree browses. One set exists per ACTIVE kubernetes context — sets are
// started lazily when the user enters a cluster and stopped when they leave
// (see Manager.ActivateContext), so watch streams are only ever open to the
// one production cluster on screen.
//
// Sync is per-kind and non-blocking: each accessor returns nil until that
// kind's initial List+Watch completed, and callers fall back to a direct API
// read on nil. RBAC denying Watch on one kind (Secrets is the usual suspect)
// therefore degrades exactly that kind to direct Lists while everything else
// stays cache-served.
type informerSet struct {
	factory  informers.SharedInformerFactory
	stop     chan struct{}
	stopOnce sync.Once
	startMu  sync.Mutex

	podIndex     toolscache.TypedIndexer[*corev1.Pod]
	pods         kindCache[corev1listers.PodLister]
	nodes        kindCache[corev1listers.NodeLister]
	namespaces   kindCache[corev1listers.NamespaceLister]
	services     kindCache[corev1listers.ServiceLister]
	configmaps   kindCache[corev1listers.ConfigMapLister]
	secrets      kindCache[corev1listers.SecretLister]
	pvcs         kindCache[corev1listers.PersistentVolumeClaimLister]
	deployments  kindCache[appsv1listers.DeploymentLister]
	replicasets  kindCache[appsv1listers.ReplicaSetLister]
	statefulsets kindCache[appsv1listers.StatefulSetLister]
	daemonsets   kindCache[appsv1listers.DaemonSetLister]
	jobs         kindCache[batchv1listers.JobLister]
	cronjobs     kindCache[batchv1listers.CronJobLister]
	hpas         kindCache[autoscalingv2listers.HorizontalPodAutoscalerLister]
	ingresses    kindCache[networkingv1listers.IngressLister]
}

// Accessors return the kind's lister once its initial sync has landed, nil
// otherwise (including on a nil set — Client.Informers() returns nil when the
// context isn't active). Callers treat nil as "use the direct API path".
//
// Objects returned by these listers are SHARED with the informer cache:
// read-only, never mutate. Copy (`*p`) before editing.

func (s *informerSet) Pods() corev1listers.PodLister {
	if s == nil || !s.pods.ready() {
		return nil
	}
	return s.pods.lister
}

// PodIndex exposes indexed reads only after the pod cache is synchronized.
func (s *informerSet) PodIndex() toolscache.TypedIndexer[*corev1.Pod] {
	if s == nil || !s.pods.ready() {
		return nil
	}
	return s.podIndex
}

const podNodeIndex = "safina/nodeName"

func podNodeIndexKeys(pod *corev1.Pod) ([]string, error) {
	return []string{pod.Spec.NodeName}, nil
}

func (s *informerSet) Nodes() corev1listers.NodeLister {
	if s == nil || !s.nodes.ready() {
		return nil
	}
	return s.nodes.lister
}

func (s *informerSet) Namespaces() corev1listers.NamespaceLister {
	if s == nil || !s.namespaces.ready() {
		return nil
	}
	return s.namespaces.lister
}

func (s *informerSet) Services() corev1listers.ServiceLister {
	if s == nil || !s.services.ready() {
		return nil
	}
	return s.services.lister
}

func (s *informerSet) ConfigMaps() corev1listers.ConfigMapLister {
	if s == nil || !s.configmaps.ready() {
		return nil
	}
	return s.configmaps.lister
}

func (s *informerSet) Secrets() corev1listers.SecretLister {
	if s == nil || !s.secrets.ready() {
		return nil
	}
	return s.secrets.lister
}

func (s *informerSet) PVCs() corev1listers.PersistentVolumeClaimLister {
	if s == nil || !s.pvcs.ready() {
		return nil
	}
	return s.pvcs.lister
}

func (s *informerSet) Deployments() appsv1listers.DeploymentLister {
	if s == nil || !s.deployments.ready() {
		return nil
	}
	return s.deployments.lister
}

func (s *informerSet) ReplicaSets() appsv1listers.ReplicaSetLister {
	if s == nil || !s.replicasets.ready() {
		return nil
	}
	return s.replicasets.lister
}

func (s *informerSet) StatefulSets() appsv1listers.StatefulSetLister {
	if s == nil || !s.statefulsets.ready() {
		return nil
	}
	return s.statefulsets.lister
}

func (s *informerSet) DaemonSets() appsv1listers.DaemonSetLister {
	if s == nil || !s.daemonsets.ready() {
		return nil
	}
	return s.daemonsets.lister
}

func (s *informerSet) Jobs() batchv1listers.JobLister {
	if s == nil || !s.jobs.ready() {
		return nil
	}
	return s.jobs.lister
}

func (s *informerSet) CronJobs() batchv1listers.CronJobLister {
	if s == nil || !s.cronjobs.ready() {
		return nil
	}
	return s.cronjobs.lister
}

func (s *informerSet) HPAs() autoscalingv2listers.HorizontalPodAutoscalerLister {
	if s == nil || !s.hpas.ready() {
		return nil
	}
	return s.hpas.lister
}

func (s *informerSet) Ingresses() networkingv1listers.IngressLister {
	if s == nil || !s.ingresses.ready() {
		return nil
	}
	return s.ingresses.lister
}

// stripCacheObject is the informer transform: it trims every cached object
// down to what the TUI actually renders, before the object enters the store.
//
//   - managedFields and the last-applied annotation are dead weight (often
//     30-50% of a pod's bytes) — safina reads neither. Inspect/edit paths do
//     their own direct Gets, so full objects are still available on demand.
//   - Secret and ConfigMap VALUES are zeroed, keys kept: the tree and the
//     inspector show key names, counts, and Type — never payloads. Reveal is
//     a direct Get (RevealSecretYAML), so secret material never sits in
//     process memory for the lifetime of a session.
//
// Safe to mutate in place: since k8s 1.27 the transform sees the object
// before any other actor.
func stripCacheObject(obj any) (any, error) {
	// Deletions can arrive as tombstones wrapping the last-known object.
	if tomb, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
		if inner, err := stripCacheObject(tomb.Obj); err == nil {
			tomb.Obj = inner
		}
		return tomb, nil
	}

	if m, ok := obj.(metav1.Object); ok {
		m.SetManagedFields(nil)
		if ann := m.GetAnnotations(); ann != nil {
			delete(ann, lastAppliedAnnotation)
		}
	}

	switch o := obj.(type) {
	case *corev1.Secret:
		for k := range o.Data {
			o.Data[k] = nil
		}
	case *corev1.ConfigMap:
		for k := range o.Data {
			o.Data[k] = ""
		}
		for k := range o.BinaryData {
			o.BinaryData[k] = nil
		}
	}
	return obj, nil
}

// startInformers builds the factory, registers every tree-browsed kind, and
// starts core watches. Auxiliary resources start on first use. It does NOT
// block on sync: each kind flips its flag from a background goroutine.
// Accessors return nil until
// then — callers fall back to direct API reads, so entering a cluster renders
// immediately while the cache warms behind the first paint.
func startInformers(cs kubernetes.Interface) *informerSet {
	factory := informers.NewSharedInformerFactoryWithOptions(
		cs, 0, informers.WithTransform(stripCacheObject),
	)
	set := &informerSet{factory: factory, stop: make(chan struct{})}

	pods := factory.Core().V1().Pods()
	set.pods.lister = pods.Lister()
	// Register before Start: the informer maintains membership on add/update/delete.
	typedPods := toolscache.NewTypedSharedIndexInformer[*corev1.Pod](pods.Informer())
	if err := typedPods.AddTypedIndexers(toolscache.TypedIndexers[*corev1.Pod]{podNodeIndex: podNodeIndexKeys}); err != nil {
		debug.Errf("informer.index_failed", "kind", "Pod", "err", err.Error())
	} else {
		set.podIndex = typedPods.GetTypedIndexer()
	}
	nodes := factory.Core().V1().Nodes()
	set.nodes.lister = nodes.Lister()
	namespaces := factory.Core().V1().Namespaces()
	set.namespaces.lister = namespaces.Lister()
	deferInformer(set, &set.services, "Service", func() (corev1listers.ServiceLister, toolscache.SharedIndexInformer) {
		informer := factory.Core().V1().Services()
		return informer.Lister(), informer.Informer()
	})
	deferInformer(set, &set.configmaps, "ConfigMap", func() (corev1listers.ConfigMapLister, toolscache.SharedIndexInformer) {
		informer := factory.Core().V1().ConfigMaps()
		return informer.Lister(), informer.Informer()
	})
	deferInformer(set, &set.secrets, "Secret", func() (corev1listers.SecretLister, toolscache.SharedIndexInformer) {
		informer := factory.Core().V1().Secrets()
		return informer.Lister(), informer.Informer()
	})
	deferInformer(set, &set.pvcs, "PersistentVolumeClaim", func() (corev1listers.PersistentVolumeClaimLister, toolscache.SharedIndexInformer) {
		informer := factory.Core().V1().PersistentVolumeClaims()
		return informer.Lister(), informer.Informer()
	})
	deployments := factory.Apps().V1().Deployments()
	set.deployments.lister = deployments.Lister()
	// ReplicaSets exist solely for the Deployment → ReplicaSet → Pod owner
	// walk (ListPodsForApp) — the tree never renders them directly.
	replicasets := factory.Apps().V1().ReplicaSets()
	set.replicasets.lister = replicasets.Lister()
	statefulsets := factory.Apps().V1().StatefulSets()
	set.statefulsets.lister = statefulsets.Lister()
	daemonsets := factory.Apps().V1().DaemonSets()
	set.daemonsets.lister = daemonsets.Lister()
	jobs := factory.Batch().V1().Jobs()
	set.jobs.lister = jobs.Lister()
	cronjobs := factory.Batch().V1().CronJobs()
	set.cronjobs.lister = cronjobs.Lister()
	hpas := factory.Autoscaling().V2().HorizontalPodAutoscalers()
	set.hpas.lister = hpas.Lister()
	deferInformer(set, &set.ingresses, "Ingress", func() (networkingv1listers.IngressLister, toolscache.SharedIndexInformer) {
		informer := factory.Networking().V1().Ingresses()
		return informer.Lister(), informer.Informer()
	})

	regs := []struct {
		kind   string
		inf    toolscache.SharedIndexInformer
		synced *atomic.Bool
	}{
		{"Pod", pods.Informer(), &set.pods.synced},
		{"Node", nodes.Informer(), &set.nodes.synced},
		{"Namespace", namespaces.Informer(), &set.namespaces.synced},
		{"Deployment", deployments.Informer(), &set.deployments.synced},
		{"ReplicaSet", replicasets.Informer(), &set.replicasets.synced},
		{"StatefulSet", statefulsets.Informer(), &set.statefulsets.synced},
		{"DaemonSet", daemonsets.Informer(), &set.daemonsets.synced},
		{"Job", jobs.Informer(), &set.jobs.synced},
		{"CronJob", cronjobs.Informer(), &set.cronjobs.synced},
		{"HorizontalPodAutoscaler", hpas.Informer(), &set.hpas.synced},
	}

	// Route watch failures (RBAC Forbidden, apiserver timeouts) to the debug
	// sink, once per kind — the reflector retries forever with capped backoff
	// and would otherwise spam. Must be registered before Start.
	for i := range regs {
		r := &regs[i]
		configureWatchErrors(r.inf, r.kind)
	}

	factory.Start(set.stop)

	for i := range regs {
		r := regs[i]
		go func() {
			// Returns false when set.stop closes first — flag stays false and
			// that kind serves direct reads for the rest of the set's life.
			if toolscache.WaitForCacheSync(set.stop, r.inf.HasSynced) {
				r.synced.Store(true)
			}
		}()
	}
	return set
}

// close shuts down the watch streams. Safe to call on a nil set or twice.
func (s *informerSet) close() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
}

// deferInformer registers a non-blocking first-use initializer. Factory.Start
// is idempotent and starts only newly requested informers; it never restarts
// the already running core caches.
func deferInformer[L any](set *informerSet, kind *kindCache[L], name string, build func() (L, toolscache.SharedIndexInformer)) {
	kind.start = func() {
		set.startMu.Lock()
		defer set.startMu.Unlock()
		select {
		case <-set.stop:
			return
		default:
		}
		lister, informer := build()
		kind.lister = lister
		configureWatchErrors(informer, name)
		set.factory.Start(set.stop)
		go func() {
			if toolscache.WaitForCacheSync(set.stop, informer.HasSynced) {
				kind.synced.Store(true)
			}
		}()
	}
}

func configureWatchErrors(informer toolscache.SharedIndexInformer, name string) {
	var once sync.Once
	_ = informer.SetWatchErrorHandler(func(_ *toolscache.Reflector, err error) {
		once.Do(func() { debug.Errf("informer.watch_failed", "kind", name, "err", err.Error()) })
	})
}
