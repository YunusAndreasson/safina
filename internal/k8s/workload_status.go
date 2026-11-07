package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// WorkloadProgress uses controller-observed status, rather than treating a
// successful write as completion. Controller failure conditions remain visible.
func WorkloadProgress(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string, rollout bool) (string, bool, error) {
	return WorkloadProgressForGeneration(ctx, cs, namespace, kind, name, rollout, "", "")
}

var ErrOperationSuperseded = errors.New("resource identity or generation changed")

func WorkloadProgressForGeneration(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string, rollout bool, uid, generation string) (string, bool, error) {
	switch kind {
	case AppKindDeployment:
		d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, err
		}
		if !expectedWorkload(d, uid, generation) {
			return "", false, ErrOperationSuperseded
		}
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		text := fmt.Sprintf("%d/%d ready · %d updated", d.Status.AvailableReplicas, desired, d.Status.UpdatedReplicas)
		if d.Status.ObservedGeneration < d.Generation {
			return "waiting for controller · " + text, false, nil
		}
		for _, c := range d.Status.Conditions {
			if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
				return text, false, errors.New("rollout exceeded its progress deadline")
			}
		}
		return text, d.Status.UpdatedReplicas == desired && d.Status.Replicas == desired && d.Status.AvailableReplicas == desired, nil
	case AppKindStatefulSet:
		d, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, err
		}
		if !expectedWorkload(d, uid, generation) {
			return "", false, ErrOperationSuperseded
		}
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		text := fmt.Sprintf("%d/%d ready", d.Status.ReadyReplicas, desired)
		if rollout && d.Spec.UpdateStrategy.Type == appsv1.OnDeleteStatefulSetStrategyType {
			return text, false, errors.New("OnDelete strategy requires manual pod replacement")
		}
		partition := int32(0)
		if d.Spec.UpdateStrategy.RollingUpdate != nil && d.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
			partition = *d.Spec.UpdateStrategy.RollingUpdate.Partition
		}
		updated := max(int32(0), desired-partition)
		return text, d.Status.ObservedGeneration >= d.Generation && d.Status.ReadyReplicas == desired && d.Status.Replicas == desired && (!rollout || (d.Status.UpdatedReplicas >= updated && (partition > 0 || desired == 0 || d.Status.CurrentRevision == d.Status.UpdateRevision))), nil
	case AppKindDaemonSet:
		d, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", false, err
		}
		if !expectedWorkload(d, uid, generation) {
			return "", false, ErrOperationSuperseded
		}
		text := fmt.Sprintf("%d/%d ready", d.Status.NumberAvailable, d.Status.DesiredNumberScheduled)
		if d.Spec.UpdateStrategy.Type == appsv1.OnDeleteDaemonSetStrategyType {
			return text, false, errors.New("OnDelete strategy requires manual pod replacement")
		}
		return text, d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedNumberScheduled == d.Status.DesiredNumberScheduled && d.Status.NumberAvailable == d.Status.DesiredNumberScheduled && d.Status.NumberMisscheduled == 0, nil
	default:
		return "", false, fmt.Errorf("progress unavailable for %s", kind)
	}
}

func expectedWorkload(obj metav1.Object, uid, generation string) bool {
	return (uid == "" || string(obj.GetUID()) == uid) && (generation == "" || strconv.FormatInt(obj.GetGeneration(), 10) == generation)
}
