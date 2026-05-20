package testutil_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/yunus/safina/internal/k8s/testutil"
)

func TestLoadPod_RoundTripsAllFields(t *testing.T) {
	p := testutil.LoadPod(t, "po_running")
	require.NotNil(t, p)

	assert.Equal(t, "api-7d4f5", p.Name)
	assert.Equal(t, "shop", p.Namespace)
	assert.Equal(t, corev1.PodRunning, p.Status.Phase)
	require.Len(t, p.OwnerReferences, 1)
	assert.Equal(t, "ReplicaSet", p.OwnerReferences[0].Kind)
	require.Len(t, p.Status.ContainerStatuses, 1)
	assert.True(t, p.Status.ContainerStatuses[0].Ready)
}

func TestLoadPod_TerminatingHasDeletionTimestamp(t *testing.T) {
	p := testutil.LoadPod(t, "po_terminating")
	require.NotNil(t, p.DeletionTimestamp,
		"terminating fixture must carry DeletionTimestamp — drain & status code branches on it")
}

func TestLoadPod_CrashLoopHasWaitingReason(t *testing.T) {
	p := testutil.LoadPod(t, "po_crashloop")
	require.Len(t, p.Status.ContainerStatuses, 1)
	w := p.Status.ContainerStatuses[0].State.Waiting
	require.NotNil(t, w)
	assert.Equal(t, "CrashLoopBackOff", w.Reason)
}

func TestLoadNode_RoundTrips(t *testing.T) {
	n := testutil.LoadNode(t, "no_ready")
	require.NotNil(t, n)
	assert.Equal(t, "node-1", n.Name)
	require.NotEmpty(t, n.Status.Conditions)
}

func TestLoadUnstructured_RoundTrips(t *testing.T) {
	u := testutil.LoadUnstructured(t, "po_running")
	require.NotNil(t, u)
	assert.Equal(t, "Pod", u.GetKind())
	assert.Equal(t, "api-7d4f5", u.GetName())
}
