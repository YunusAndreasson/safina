package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
)

func TestDeploymentProgressWaitsForObservedRollout(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		observed              int64
		updated, ready, total int32
		done                  bool
		failed                bool
	}{
		{name: "write accepted but not observed", observed: 1, updated: 3, ready: 3, total: 3},
		{name: "old pods still available", observed: 2, updated: 1, ready: 3, total: 4},
		{name: "new pods unavailable", observed: 2, updated: 3, ready: 2, total: 3},
		{name: "complete", observed: 2, updated: 3, ready: 3, total: 3, done: true},
		{name: "deadline", observed: 2, updated: 1, ready: 1, total: 3, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop", Generation: 2}, Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(3))}, Status: appsv1.DeploymentStatus{ObservedGeneration: tc.observed, UpdatedReplicas: tc.updated, AvailableReplicas: tc.ready, Replicas: tc.total}}
			if tc.failed {
				d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}
			}
			text, done, err := WorkloadProgress(t.Context(), fake.NewClientset(d), "shop", AppKindDeployment, "web", true)
			require.NotEmpty(t, text)
			require.Equal(t, tc.done, done)
			if tc.failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestStatefulProgressHonorsPartition(t *testing.T) {
	s := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "shop", Generation: 2}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To(int32(3)), UpdateStrategy: appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType, RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(int32(1))}}}, Status: appsv1.StatefulSetStatus{ObservedGeneration: 2, Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 2, CurrentRevision: "old", UpdateRevision: "new"}}
	_, done, err := WorkloadProgress(t.Context(), fake.NewClientset(s), "shop", AppKindStatefulSet, "db", true)
	require.NoError(t, err)
	require.True(t, done)
	s.Spec.UpdateStrategy.RollingUpdate.Partition = ptr.To(int32(0))
	_, done, err = WorkloadProgress(t.Context(), fake.NewClientset(s), "shop", AppKindStatefulSet, "db", true)
	require.NoError(t, err)
	require.False(t, done)
}
