package k8s

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

func int32PtrVal(r *int32) int32 {
	if r == nil {
		return 0
	}
	return *r
}

func ScaleDeployment(ctx context.Context, cs kubernetes.Interface, namespace, name string, replicas int32) error {
	scale, err := cs.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale: %w", err)
	}
	scale.Spec.Replicas = replicas
	result, err := cs.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	RecordMutationResponse(ctx, result, err)
	return err
}

// ScaleWorkload uses the scale subresource belonging to the selected kind.
func ScaleWorkload(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string, replicas int32) error {
	if replicas < 0 {
		return errors.New("replica count must not be negative")
	}
	switch kind {
	case AppKindDeployment:
		return ScaleDeployment(ctx, cs, namespace, name, replicas)
	case AppKindStatefulSet:
		scale, err := cs.AppsV1().StatefulSets(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get scale: %w", err)
		}
		scale.Spec.Replicas = replicas
		result, err := cs.AppsV1().StatefulSets(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
		RecordMutationResponse(ctx, result, err)
		return err
	default:
		return fmt.Errorf("scaling %s is not supported", kind)
	}
}

// RestartWorkload triggers a rolling restart by stamping a fresh
// kubectl.kubernetes.io/restartedAt annotation on the pod template — the
// same mechanism `kubectl rollout restart` uses. Supported for Deployment,
// StatefulSet, and DaemonSet.
func RestartWorkload(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) error {
	patch := []byte(fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		time.Now().UTC().Format(time.RFC3339),
	))
	switch kind {
	case "Deployment":
		result, err := cs.AppsV1().Deployments(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
		RecordMutationResponse(ctx, result, err)
		return err
	case "StatefulSet":
		result, err := cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
		RecordMutationResponse(ctx, result, err)
		return err
	case "DaemonSet":
		result, err := cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
		RecordMutationResponse(ctx, result, err)
		return err
	}
	return fmt.Errorf("restart not supported for kind %q", kind)
}

func RollbackDeployment(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	deployment, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	rsList, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(deployment.Spec.Selector),
	})
	if err != nil {
		return fmt.Errorf("failed to list replicasets: %w", err)
	}

	currentRev, err := strconv.ParseInt(deployment.Annotations["deployment.kubernetes.io/revision"], 10, 64)
	if err != nil || currentRev < 1 {
		return errors.New("deployment has no valid current revision")
	}
	var targetRSName string
	var targetRevision int64
	for _, rs := range rsList.Items {
		if !metav1.IsControlledBy(&rs, deployment) {
			continue
		}
		revision, parseErr := strconv.ParseInt(rs.Annotations["deployment.kubernetes.io/revision"], 10, 64)
		if parseErr == nil && revision > targetRevision && revision < currentRev {
			targetRSName = rs.Name
			targetRevision = revision
		}
	}

	if targetRSName == "" {
		return errors.New("no previous revision found to rollback to")
	}

	targetRS, err := cs.AppsV1().ReplicaSets(namespace).Get(ctx, targetRSName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get target replicaset: %w", err)
	}

	latest := deployment.DeepCopy()
	latest.Spec.Template = *targetRS.Spec.Template.DeepCopy()
	delete(latest.Spec.Template.Labels, "pod-template-hash")
	result, err := cs.AppsV1().Deployments(namespace).Update(ctx, latest, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment for rollback: %w", err)
	}

	RecordMutationResponse(ctx, result, nil)
	return nil
}
