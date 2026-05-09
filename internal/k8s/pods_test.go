package k8s

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodToInfo_StatusPriority(t *testing.T) {
	now := metav1.NewTime(time.Now())

	cases := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{
			name: "running_healthy",
			pod: corev1.Pod{
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			},
			want: "Running",
		},
		{
			name: "crashloop_overrides_phase",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
					},
				},
			},
			want: "CrashLoopBackOff",
		},
		{
			name: "imagepullbackoff_surfaces",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					ContainerStatuses: []corev1.ContainerStatus{
						{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}},
					},
				},
			},
			want: "ImagePullBackOff",
		},
		{
			// Regression: previously CrashLoopBackOff would mask Terminating
			// because container-fault inspection ran AFTER the deletion
			// check. The operator needs to know the pod is being torn down.
			name: "terminating_wins_over_crashloop",
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
		{
			name: "terminating_wins_over_running",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			want: "Terminating",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
