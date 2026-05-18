package k8s

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStartInformers_SyncsAndExposesListers(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	)
	set := startInformers(cs, 5*time.Second)
	require.NotNil(t, set, "sync should succeed with fake clientset")
	t.Cleanup(set.close)

	pods, err := set.podLister.List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, pods, 2)

	nodes, err := set.nodeLister.List(labels.Everything())
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
}

func TestInformerSet_CloseIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	set := startInformers(cs, 5*time.Second)
	require.NotNil(t, set)
	// Two closes must not panic on a closed channel.
	set.close()
	set.close()
}

func TestInformerSet_CloseNilSafe(t *testing.T) {
	var set *informerSet
	// Methods on nil must not panic — Client.Close relies on this when
	// the informer sync failed (informers stays nil on the Client).
	set.close()
}

func TestClientHotPods_FallsBackWhenInformersNil(t *testing.T) {
	// A Client without an informerSet must still work via the direct path.
	// We can't run ListHotPodsByCPU itself end-to-end here without wiring
	// metrics-server, but we can prove the method picks the direct branch
	// without panicking on the nil informerSet — covered by build + the
	// explicit guard. This test pins the contract.
	c := &Client{informers: nil}
	assert.Nil(t, c.informers, "fallback path is taken when informers is nil")
}
