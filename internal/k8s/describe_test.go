package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDescribeYAML_Deployment(t *testing.T) {
	replicas := int32(3)
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "myapp"}},
		},
	}
	cs := fake.NewSimpleClientset(dep)

	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Deployment", "myapp")
	require.NoError(t, err)
	assert.Contains(t, yaml, "myapp")
	assert.Contains(t, yaml, "apps/v1")
	assert.NotContains(t, yaml, "managedFields")
}

func TestDescribeYAML_Pod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "ns"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
	}
	cs := fake.NewSimpleClientset(pod)

	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Pod", "mypod")
	require.NoError(t, err)
	assert.Contains(t, yaml, "mypod")
	assert.Contains(t, yaml, "nginx")
	assert.Contains(t, yaml, "v1")
	assert.NotContains(t, yaml, "managedFields")
}

func TestDescribeYAML_UnsupportedKind(t *testing.T) {
	cs := fake.NewSimpleClientset()
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "CronJob", "cj")
	assert.Error(t, err)
	assert.Empty(t, yaml)
	assert.Contains(t, err.Error(), "unsupported kind")
}

func TestDescribeYAML_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	_, err := DescribeYAML(context.Background(), cs, "ns", "Deployment", "nope")
	require.Error(t, err)
}

func TestDescribeYAML_EmptyNamespace(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
	}
	cs := fake.NewSimpleClientset(pod)
	yaml, err := DescribeYAML(context.Background(), cs, "default", "Pod", "mypod")
	require.NoError(t, err)
	assert.NotEmpty(t, yaml)
}
