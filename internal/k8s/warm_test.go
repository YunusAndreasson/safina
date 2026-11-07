package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestWarmResourceCacheDoesNotActivateCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	client := &Client{Clientset: cs}
	for _, kind := range []string{KindService, KindConfigMap, KindSecret, KindPVC, KindIngress, KindPod} {
		client.WarmResourceCache(kind)
	}
	require.Nil(t, client.Informers())
	require.Empty(t, cs.Actions())
	var absent *Client
	require.NotPanics(t, func() { absent.WarmResourceCache(KindService) })
}
