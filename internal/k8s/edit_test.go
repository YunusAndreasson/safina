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

func TestBuildEditableYAML_ConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "ns"},
		Data:       map[string]string{"key": "value"},
	}
	cs := fake.NewSimpleClientset(cm)

	yaml, err := BuildEditableYAML(context.Background(), cs, EditKindConfigMap, "ns", "settings")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: ConfigMap")
	assert.Contains(t, yaml, "value")
	assert.NotContains(t, yaml, "managedFields")
}

func TestBuildEditableYAML_SecretDecodesData(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte("hunter2")},
	}
	cs := fake.NewSimpleClientset(sec)

	yaml, err := BuildEditableYAML(context.Background(), cs, EditKindSecret, "ns", "creds")
	require.NoError(t, err)
	assert.Contains(t, yaml, "stringData")
	assert.Contains(t, yaml, "hunter2")
}

func TestBuildEditableYAML_Unsupported(t *testing.T) {
	cs := fake.NewSimpleClientset()
	_, err := BuildEditableYAML(context.Background(), cs, "Pod", "ns", "anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported kind")
}

func TestApplyEditedYAML_ConfigMap(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "ns", ResourceVersion: "1"},
		Data:       map[string]string{"key": "old"},
	}
	cs := fake.NewSimpleClientset(cm)

	edited := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: ns
  resourceVersion: "1"
data:
  key: new
`)
	require.NoError(t, ApplyEditedYAML(context.Background(), cs, EditKindConfigMap, "ns", "settings", edited))

	got, err := cs.CoreV1().ConfigMaps("ns").Get(context.Background(), "settings", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "new", got.Data["key"])
}

func TestApplyEditedYAML_RejectsRename(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "settings", Namespace: "ns", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(cm)

	// Editor "rename" — name changed in the doc.
	edited := []byte(`apiVersion: v1
kind: ConfigMap
metadata:
  name: renamed
  namespace: ns
data: {}
`)
	err := ApplyEditedYAML(context.Background(), cs, EditKindConfigMap, "ns", "settings", edited)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name/namespace was changed")
}

func TestApplyEditedYAML_RejectsBadYAML(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
	})
	err := ApplyEditedYAML(context.Background(), cs, EditKindConfigMap, "ns", "x", []byte("not: : valid: yaml: ["))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml parse")
}

func TestIsEditableKind(t *testing.T) {
	for _, k := range []string{
		EditKindDeployment, EditKindStatefulSet, EditKindDaemonSet,
		EditKindConfigMap, EditKindSecret,
		EditKindService, EditKindIngress, EditKindPVC,
	} {
		assert.True(t, IsEditableKind(k), k)
	}
	for _, k := range []string{"Pod", "Node", "Namespace", "PersistentVolume", "Job", "CronJob", ""} {
		assert.False(t, IsEditableKind(k), k)
	}
}

func TestBuildEditableYAML_StatefulSet(t *testing.T) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(ss)

	yaml, err := BuildEditableYAML(context.Background(), cs, EditKindStatefulSet, "ns", "db")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: StatefulSet")
	assert.Contains(t, yaml, "apiVersion: apps/v1")
	assert.NotContains(t, yaml, "managedFields")
}

func TestBuildEditableYAML_DaemonSet(t *testing.T) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "ns"},
	}
	cs := fake.NewSimpleClientset(ds)

	yaml, err := BuildEditableYAML(context.Background(), cs, EditKindDaemonSet, "ns", "node-agent")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: DaemonSet")
	assert.Contains(t, yaml, "apiVersion: apps/v1")
	assert.NotContains(t, yaml, "managedFields")
}

func TestApplyEditedYAML_StatefulSetRejectsRename(t *testing.T) {
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "ns", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(ss)

	edited := []byte(`apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db-renamed
  namespace: ns
`)
	err := ApplyEditedYAML(context.Background(), cs, EditKindStatefulSet, "ns", "db", edited)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name/namespace was changed")
}

func TestApplyEditedYAML_DaemonSetRoundTrip(t *testing.T) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "node-agent", Namespace: "ns", ResourceVersion: "1"},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent:1.0"}}},
			},
		},
	}
	cs := fake.NewSimpleClientset(ds)

	edited := []byte(`apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: node-agent
  namespace: ns
  resourceVersion: "1"
spec:
  selector: {}
  template:
    metadata: {}
    spec:
      containers:
      - name: agent
        image: agent:2.0
`)
	require.NoError(t, ApplyEditedYAML(context.Background(), cs, EditKindDaemonSet, "ns", "node-agent", edited))

	got, err := cs.AppsV1().DaemonSets("ns").Get(context.Background(), "node-agent", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "agent:2.0", got.Spec.Template.Spec.Containers[0].Image)
}

func TestApplyEditedYAML_SecretRoundTrip(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns", ResourceVersion: "1"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"password": []byte("old")},
	}
	cs := fake.NewSimpleClientset(sec)

	edited := []byte(`apiVersion: v1
kind: Secret
metadata:
  name: creds
  namespace: ns
  resourceVersion: "1"
type: Opaque
stringData:
  password: new
`)
	require.NoError(t, ApplyEditedYAML(context.Background(), cs, EditKindSecret, "ns", "creds", edited))

	got, err := cs.CoreV1().Secrets("ns").Get(context.Background(), "creds", metav1.GetOptions{})
	require.NoError(t, err)
	// Fake client doesn't auto-merge stringData into data the way the real API
	// server does, but the value lands in StringData where the real server
	// would consume it. Either field reaching this point is the win.
	if got.StringData["password"] != "new" && string(got.Data["password"]) != "new" {
		t.Fatalf("expected password=new in stringData or data, got %#v / %#v", got.StringData, got.Data)
	}
}
