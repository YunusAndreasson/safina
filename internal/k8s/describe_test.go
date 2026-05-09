package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDescribeYAML_Deployment(t *testing.T) {
	replicas := int32(3)
	dep := &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
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
	// "ServiceAccount" stays out of the describe set today — the TUI
	// doesn't surface it as a tree row, so describing one would have
	// no entry point. If that ever changes, swap this fixture to a
	// genuinely unknown kind.
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "ServiceAccount", "sa")
	require.Error(t, err)
	assert.Empty(t, yaml)
	assert.Contains(t, err.Error(), "unsupported kind")
}

func TestDescribeYAML_StatefulSet(t *testing.T) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(ss)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "StatefulSet", "db")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: StatefulSet")
	assert.Contains(t, yaml, "apps/v1")
}

func TestDescribeYAML_DaemonSet(t *testing.T) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(ds)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "DaemonSet", "agent")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: DaemonSet")
}

func TestDescribeYAML_CronJob(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "ns"},
		Spec:       batchv1.CronJobSpec{Schedule: "0 0 * * *"},
	}
	cs := fake.NewSimpleClientset(cj)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "CronJob", "nightly")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: CronJob")
	assert.Contains(t, yaml, "0 0 * * *")
}

func TestDescribeYAML_Job(t *testing.T) {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(j)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Job", "migrate")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Job")
}

func TestDescribeYAML_ConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "ns"},
		Data:       map[string]string{"key": "value"},
	}
	cs := fake.NewSimpleClientset(cm)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "ConfigMap", "settings")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: ConfigMap")
	assert.Contains(t, yaml, "value")
}

func TestDescribeYAML_Secret(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"token": []byte("abc")},
	}
	cs := fake.NewSimpleClientset(sec)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Secret", "tls")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Secret")
	// Secret describe stays base64-encoded — operator uses Reveal for
	// plaintext. Asserting the base64 of "abc" ("YWJj") rules out an
	// accidental decode.
	assert.Contains(t, yaml, "YWJj")
}

func TestDescribeYAML_Service(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
	}
	cs := fake.NewSimpleClientset(svc)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Service", "api")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Service")
}

func TestDescribeYAML_Ingress(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(ing)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "Ingress", "web")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Ingress")
	assert.Contains(t, yaml, "networking.k8s.io/v1")
}

func TestDescribeYAML_PVC(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(pvc)
	yaml, err := DescribeYAML(context.Background(), cs, "ns", "PersistentVolumeClaim", "data")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: PersistentVolumeClaim")
}

func TestDescribeYAML_PV(t *testing.T) {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "vol-1"},
	}
	cs := fake.NewSimpleClientset(pv)
	// PV is cluster-scoped — no namespace.
	yaml, err := DescribeYAML(context.Background(), cs, "", "PersistentVolume", "vol-1")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: PersistentVolume")
}

func TestDescribeYAML_Node(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
	}
	cs := fake.NewSimpleClientset(n)
	yaml, err := DescribeYAML(context.Background(), cs, "", "Node", "worker-1")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Node")
}

func TestDescribeYAML_Namespace(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "shop"},
	}
	cs := fake.NewSimpleClientset(ns)
	yaml, err := DescribeYAML(context.Background(), cs, "", "Namespace", "shop")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Namespace")
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
