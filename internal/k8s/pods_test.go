package k8s

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// House style for table-driven tests: map[string]struct{...} keyed by the
// subtest name. Gives randomized iteration order (so order-coupled tests
// fail loudly) and removes the redundant `name` field. New table tests
// should follow this shape.
func TestPodToInfo_StatusPriority(t *testing.T) {
	now := metav1.NewTime(time.Now())

	cases := map[string]struct {
		pod  corev1.Pod
		want string
	}{
		"running_healthy": {
			pod:  corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}},
			want: "Running",
		},
		"crashloop_overrides_phase": {
			pod: corev1.Pod{Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
				},
			}},
			want: "CrashLoopBackOff",
		},
		"imagepullbackoff_surfaces": {
			pod: corev1.Pod{Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				ContainerStatuses: []corev1.ContainerStatus{
					{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
				},
			}},
			want: "ImagePullBackOff",
		},
		// Regression: previously CrashLoopBackOff would mask Terminating
		// because container-fault inspection ran AFTER the deletion check.
		// The operator needs to know the pod is being torn down.
		"terminating_wins_over_crashloop": {
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
					},
				},
			},
			want: "Terminating",
		},
		"terminating_wins_over_running": {
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			want: "Terminating",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := podToInfo(tc.pod)
			if got.Status != tc.want {
				t.Errorf("podToInfo Status = %q, want %q", got.Status, tc.want)
			}
		})
	}
}

func TestPodToInfo_RestartSum(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{RestartCount: 3},
				{RestartCount: 5},
				{RestartCount: 0},
			},
		},
	}
	got := podToInfo(pod)
	if got.Restarts != 8 {
		t.Errorf("Restarts = %d, want 8", got.Restarts)
	}
}

func TestPodToInfo_Evicted(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{
			Phase:  corev1.PodFailed,
			Reason: "Evicted",
		},
	}
	got := podToInfo(pod)
	assert.Equal(t, "Evicted", got.Status)
}

func TestPodToInfo_OOMKilledContainer(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason: "OOMKilled",
						},
					},
				},
			},
		},
	}
	got := podToInfo(pod)
	assert.Equal(t, "OOMKilled", got.Status)
}

func TestPodToInfo_MultiContainerMixed(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
	got := podToInfo(pod)
	// Should surface CrashLoopBackOff even though one container is running
	if got.Status != "CrashLoopBackOff" {
		t.Errorf("Status = %q, want CrashLoopBackOff", got.Status)
	}
}

func TestPodToInfo_NodeName(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	got := podToInfo(pod)
	if got.NodeName != "node-1" {
		t.Errorf("NodeName = %q, want node-1", got.NodeName)
	}
}

func TestPodToInfo_Unscheduled(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	got := podToInfo(pod)
	if got.NodeName != "" {
		t.Errorf("unscheduled pod should have empty NodeName, got %q", got.NodeName)
	}
}

func TestPodToInfo_PendingWithContainerCreating(t *testing.T) {
	pod := corev1.Pod{
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
					},
				},
			},
		},
	}
	got := podToInfo(pod)
	// ContainerCreating is not in the override list, so status stays "Pending"
	if got.Status != "Pending" {
		t.Errorf("Status = %q, want Pending", got.Status)
	}
}
