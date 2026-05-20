package k8s

import (
	"context"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// informerSet owns the shared informer factory plus the pod + node listers
// that hot-path read methods on Client consult. One set per kubernetes
// context. Built and synced eagerly at Client creation so the first call
// from the TUI hits a warm cache; the bench harness measured ~75% / ~92%
// p95 reduction on the two paths this serves (S2 repeat-pods, S4 cluster-
// health) versus direct List.
type informerSet struct {
	factory    informers.SharedInformerFactory
	podLister  corev1listers.PodLister
	nodeLister corev1listers.NodeLister
	stop       chan struct{}
}

// startInformersSyncTimeout bounds how long startInformers will block
// on initial WaitForCacheSync. Five seconds is enough headroom for any
// real cluster the operator is connecting to (the slow case is API
// server cold starts) while keeping a misconfigured / unreachable
// cluster from hanging the launch indefinitely.
const startInformersSyncTimeout = 5 * time.Second

// startInformers builds the factory, registers Pod + Node informers, starts
// them, and blocks until both initial syncs complete or the timeout fires.
//
// Returns nil when the cache fails to sync within the deadline. The most
// likely cause in the field is RBAC denying Watch on Pods or Nodes; degrading
// to direct-API reads is preferable to refusing the cluster outright, so
// callers treat nil as "fall back to the uncached path."
func startInformers(cs kubernetes.Interface) *informerSet {
	factory := informers.NewSharedInformerFactory(cs, 0)
	podInf := factory.Core().V1().Pods()
	nodeInf := factory.Core().V1().Nodes()
	set := &informerSet{
		factory:    factory,
		podLister:  podInf.Lister(),
		nodeLister: nodeInf.Lister(),
		stop:       make(chan struct{}),
	}
	factory.Start(set.stop)

	syncCtx, cancel := context.WithTimeout(context.Background(), startInformersSyncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(),
		podInf.Informer().HasSynced,
		nodeInf.Informer().HasSynced,
	) {
		close(set.stop)
		return nil
	}
	return set
}

// close shuts down the watch streams. Safe to call on a nil set or twice.
func (s *informerSet) close() {
	if s == nil {
		return
	}
	select {
	case <-s.stop:
		// already closed
	default:
		close(s.stop)
	}
}
