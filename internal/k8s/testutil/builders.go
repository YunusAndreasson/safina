package testutil

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodOpt mutates a Pod fixture. Combine to express test variants
// concisely:
//
//	p := testutil.Pod("evil", testutil.WithTerminating(), testutil.WithOrphan())
type PodOpt func(*corev1.Pod)

// Pod returns a minimal Pod owned by a ReplicaSet on node "n1", which is
// the shape most drain / scheduling tests want as a starting point.
func Pod(name string, opts ...PodOpt) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       "default",
			OwnerReferences: OwnedByReplicaSet(),
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func WithOrphan() PodOpt { return func(p *corev1.Pod) { p.OwnerReferences = nil } }
func WithOwnedByDaemonSet() PodOpt {
	return func(p *corev1.Pod) { p.OwnerReferences = OwnedByDaemonSet() }
}

func WithTerminating() PodOpt {
	return func(p *corev1.Pod) {
		now := metav1.NewTime(time.Now())
		p.DeletionTimestamp = &now
	}
}

func WithEmptyDirVolume(name string) PodOpt {
	return func(p *corev1.Pod) {
		p.Spec.Volumes = append(p.Spec.Volumes, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}
}

func OwnedByReplicaSet() []metav1.OwnerReference {
	ctrl := true
	return []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs", Controller: &ctrl}}
}

func OwnedByDaemonSet() []metav1.OwnerReference {
	ctrl := true
	return []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "ds", Controller: &ctrl}}
}
