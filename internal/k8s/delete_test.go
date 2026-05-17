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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestDeleteResource_AllKinds covers every kind the dispatcher claims to
// support. The shared shape: seed a fake client with one object, delete it,
// confirm Get returns NotFound. Tracking-cassette style: one assertion per
// kind so a regression points at the exact case.
func TestDeleteResource_AllKinds(t *testing.T) {
	ctx := context.Background()
	ns := "ns"
	name := "x"
	meta := metav1.ObjectMeta{Name: name, Namespace: ns}

	cases := []struct {
		kind     string
		seed     func() *fake.Clientset
		gone     func(*fake.Clientset) error
		altKinds []string // alternate kind strings (alias inputs) the dispatcher must also accept
	}{
		{
			kind: KindPod,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: AppKindDeployment,
			seed: func() *fake.Clientset {
				return fake.NewSimpleClientset(&appsv1.Deployment{ObjectMeta: meta})
			},
			gone: func(cs *fake.Clientset) error {
				_, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: AppKindStatefulSet,
			seed: func() *fake.Clientset {
				return fake.NewSimpleClientset(&appsv1.StatefulSet{ObjectMeta: meta})
			},
			gone: func(cs *fake.Clientset) error {
				_, err := cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: AppKindDaemonSet,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&appsv1.DaemonSet{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: AppKindJob,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&batchv1.Job{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.BatchV1().Jobs(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: AppKindCronJob,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&batchv1.CronJob{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: KindConfigMap,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&corev1.ConfigMap{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: KindSecret,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: KindService,
			seed: func() *fake.Clientset { return fake.NewSimpleClientset(&corev1.Service{ObjectMeta: meta}) },
			gone: func(cs *fake.Clientset) error {
				_, err := cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: KindIngress,
			seed: func() *fake.Clientset {
				return fake.NewSimpleClientset(&networkingv1.Ingress{ObjectMeta: meta})
			},
			gone: func(cs *fake.Clientset) error {
				_, err := cs.NetworkingV1().Ingresses(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
		},
		{
			kind: KindPVC,
			seed: func() *fake.Clientset {
				return fake.NewSimpleClientset(&corev1.PersistentVolumeClaim{ObjectMeta: meta})
			},
			gone: func(cs *fake.Clientset) error {
				_, err := cs.CoreV1().PersistentVolumeClaims(ns).Get(ctx, name, metav1.GetOptions{})
				return err
			},
			altKinds: []string{"PersistentVolumeClaim"}, // actionTarget returns the API name
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			cs := tc.seed()
			require.NoError(t, DeleteResource(ctx, cs, tc.kind, ns, name))
			err := tc.gone(cs)
			assert.True(t, apierrors.IsNotFound(err), "want NotFound, got %v", err)
		})
		for _, alt := range tc.altKinds {
			t.Run(tc.kind+"/alias="+alt, func(t *testing.T) {
				cs := tc.seed()
				require.NoError(t, DeleteResource(ctx, cs, alt, ns, name))
				err := tc.gone(cs)
				assert.True(t, apierrors.IsNotFound(err), "want NotFound, got %v", err)
			})
		}
	}
}

func TestDeleteResource_UnknownKind(t *testing.T) {
	cs := fake.NewSimpleClientset()
	err := DeleteResource(context.Background(), cs, "Mystery", "ns", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Mystery")
}
