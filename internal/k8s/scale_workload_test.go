package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestScaleWorkloadUsesSelectedKind(t *testing.T) {
	for kind, resource := range map[string]string{AppKindDeployment: "deployments", AppKindStatefulSet: "statefulsets"} {
		t.Run(kind, func(t *testing.T) {
			cs := fake.NewClientset()
			cs.PrependReactor("get", resource, func(action ktesting.Action) (bool, runtime.Object, error) {
				require.Equal(t, "scale", action.GetSubresource())
				require.Equal(t, "qa", action.GetNamespace())
				return true, &autoscalingv1.Scale{}, nil
			})
			cs.PrependReactor("update", resource, func(action ktesting.Action) (bool, runtime.Object, error) {
				require.Equal(t, "scale", action.GetSubresource())
				update, ok := action.(ktesting.UpdateAction)
				require.True(t, ok)
				scale, ok := update.GetObject().(*autoscalingv1.Scale)
				require.True(t, ok)
				require.EqualValues(t, 2, scale.Spec.Replicas)
				return true, scale, nil
			})
			require.NoError(t, ScaleWorkload(context.Background(), cs, "qa", kind, "web", 2))
			require.Len(t, cs.Actions(), 2)
		})
	}
	cs := fake.NewClientset()
	require.Error(t, ScaleWorkload(context.Background(), cs, "qa", AppKindDaemonSet, "web", 2))
	require.Error(t, ScaleWorkload(context.Background(), cs, "qa", AppKindStatefulSet, "web", -1))
	require.Empty(t, cs.Actions())
}
