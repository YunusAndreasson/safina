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
	"k8s.io/client-go/kubernetes/fake"
)

func TestScaleDeployment_ErrorOnNotFound(t *testing.T) {
	// fake.Clientset GetScale panics on missing Scale subresource.
	// Test the error path instead: a deployment that doesn't exist.
	cs := fake.NewSimpleClientset()
	err := ScaleDeployment(context.Background(), cs, "ns", "nope", 1)
	// The fake client's GetScale will try to get the deployment, which won't
	// be found, so it errors.
	require.Error(t, err)
}

func TestRestartWorkload_Deployment(t *testing.T) {
	cs := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{},
		},
	})

	err := RestartWorkload(context.Background(), cs, "ns", "Deployment", "myapp")
	require.NoError(t, err)

	dep, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	restartedAt, ok := dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.True(t, ok, "restartedAt annotation should be set")
	assert.NotEmpty(t, restartedAt)
	_, parseErr := time.Parse(time.RFC3339, restartedAt)
	assert.NoError(t, parseErr, "restartedAt should be valid RFC3339")
}

func TestRestartWorkload_StatefulSet(t *testing.T) {
	cs := fake.NewSimpleClientset(&appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts", Namespace: "ns"},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{},
		},
	})

	err := RestartWorkload(context.Background(), cs, "ns", "StatefulSet", "sts")
	require.NoError(t, err)

	sts, err := cs.AppsV1().StatefulSets("ns").Get(context.Background(), "sts", metav1.GetOptions{})
	require.NoError(t, err)
	_, ok := sts.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.True(t, ok)
}

func TestRestartWorkload_DaemonSet(t *testing.T) {
	cs := fake.NewSimpleClientset(&appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "ns"},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{},
		},
	})

	err := RestartWorkload(context.Background(), cs, "ns", "DaemonSet", "ds")
	require.NoError(t, err)

	ds, err := cs.AppsV1().DaemonSets("ns").Get(context.Background(), "ds", metav1.GetOptions{})
	require.NoError(t, err)
	_, ok := ds.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.True(t, ok)
}

func TestRestartWorkload_UnsupportedKind(t *testing.T) {
	cs := fake.NewSimpleClientset()
	err := RestartWorkload(context.Background(), cs, "ns", "CronJob", "cj")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
}

func TestRestartWorkload_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	err := RestartWorkload(context.Background(), cs, "ns", "Deployment", "nope")
	require.Error(t, err)
}

func TestRollbackDeployment(t *testing.T) {
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "myapp",
			Namespace:   "ns",
			UID:         "dep-uid",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "myapp"}},
			},
		},
	}
	// ReplicaSet for revision 2 (the one we should roll back to)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-v2",
			Namespace: "ns",
			UID:       "rs-uid",
			Labels:    map[string]string{"app": "myapp"},
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "2",
			},
			OwnerReferences: []metav1.OwnerReference{
				{UID: "dep-uid", Name: "myapp", Kind: "Deployment"},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "myapp", "version": "v2"}},
			},
		},
	}

	cs := fake.NewSimpleClientset(dep, rs)
	err := RollbackDeployment(context.Background(), cs, "ns", "myapp")
	require.NoError(t, err)

	updated, err := cs.AppsV1().Deployments("ns").Get(context.Background(), "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "v2", updated.Spec.Template.Labels["version"])
}

func TestRollbackDeployment_NoPreviousRevision(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "myapp",
			Namespace:   "ns",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
		},
	}
	cs := fake.NewSimpleClientset(dep)
	err := RollbackDeployment(context.Background(), cs, "ns", "myapp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no previous revision")
}

func TestRollbackDeployment_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	err := RollbackDeployment(context.Background(), cs, "ns", "nope")
	require.Error(t, err)
}

func TestInt32PtrVal_Nil(t *testing.T) {
	assert.Equal(t, int32(0), int32PtrVal(nil))
}

func TestInt32PtrVal_Value(t *testing.T) {
	v := int32(42)
	assert.Equal(t, int32(42), int32PtrVal(&v))
}
