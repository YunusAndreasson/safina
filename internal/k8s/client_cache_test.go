package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// The tree's 5s tick reads through the cache-aware Client methods. This test
// pins the core contract: rows served from the informer cache are identical
// to rows served by the direct-API path, and the direct path is used
// transparently while informers are cold.
func TestClientCache_TreeReadsMatchDirectPath(t *testing.T) {
	replicas := int32(2)
	deployUID := types.UID("deploy-uid-1")
	rsUID := types.UID("rs-uid-1")
	cs := fake.NewSimpleClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "payments"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: deployUID},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-abc", Namespace: "payments", UID: rsUID,
				OwnerReferences: []metav1.OwnerReference{{UID: deployUID}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-abc-1", Namespace: "payments",
				OwnerReferences: []metav1.OwnerReference{{UID: rsUID}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api-svc", Namespace: "payments"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "payments"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"password": []byte("hunter2")},
		},
	)
	c := &Client{Clientset: cs}
	ctx := context.Background()

	// Cold client: every method must take the direct path without informers.
	directApps, err := c.ListAppsInNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	directSecrets, err := c.ListSecretsAcrossNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	directSvcs, err := c.ListServicesAcrossNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	directUser, directSys, err := c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)
	directPods, err := c.ListPodsForApp(ctx, AppRef{
		Kind: AppKindDeployment, Namespace: "payments", Name: "api", UID: deployUID,
	})
	require.NoError(t, err)
	require.Len(t, directApps, 1)
	require.Len(t, directPods, 1)

	// Warm the cache and read everything again — rows must be identical.
	c.StartInformers()
	t.Cleanup(c.StopInformers)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		inf := c.Informers()
		if inf.Pods() != nil && inf.Deployments() != nil && inf.ReplicaSets() != nil &&
			inf.StatefulSets() != nil && inf.DaemonSets() != nil && inf.Jobs() != nil &&
			inf.CronJobs() != nil && inf.Namespaces() != nil && inf.Secrets() != nil &&
			inf.Services() != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.NotNil(t, c.Informers().Pods(), "informers must sync")

	cachedApps, err := c.ListAppsInNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	assert.Equal(t, directApps, cachedApps, "apps rows must match direct path")

	cachedSecrets, err := c.ListSecretsAcrossNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	assert.Equal(t, directSecrets, cachedSecrets, "secret rows must match despite zeroed values in cache")

	cachedSvcs, err := c.ListServicesAcrossNamespaces(ctx, []string{"payments"})
	require.NoError(t, err)
	assert.Equal(t, directSvcs, cachedSvcs)

	cachedUser, cachedSys, err := c.ListAndClassifyNamespaces(ctx, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, directUser, cachedUser)
	assert.Equal(t, directSys, cachedSys)

	cachedPods, err := c.ListPodsForApp(ctx, AppRef{
		Kind: AppKindDeployment, Namespace: "payments", Name: "api", UID: deployUID,
	})
	require.NoError(t, err)
	assert.Equal(t, directPods, cachedPods, "owner-walk pods must match direct path")

	// Reveal stays value-bearing: the direct Get must return real secret
	// bytes even though the cached copy is zeroed.
	yaml, err := RevealSecretYAML(ctx, cs, "payments", "db-creds")
	require.NoError(t, err)
	assert.Contains(t, yaml, "hunter2", "reveal must bypass the value-stripped cache")
}
