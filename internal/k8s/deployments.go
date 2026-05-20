package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	_, err = cs.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	return err
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
		_, err := cs.AppsV1().Deployments(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	case "StatefulSet":
		_, err := cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	case "DaemonSet":
		_, err := cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
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

	type rsRevision struct {
		rs       string
		revision string
	}
	var revisions []rsRevision
	for _, rs := range rsList.Items {
		if anno := rs.Annotations["deployment.kubernetes.io/revision"]; anno != "" {
			revisions = append(revisions, rsRevision{rs: rs.Name, revision: anno})
		}
	}

	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].revision < revisions[j].revision
	})

	currentRev := deployment.Annotations["deployment.kubernetes.io/revision"]
	var targetRSName string
	for i := len(revisions) - 1; i >= 0; i-- {
		if revisions[i].revision < currentRev && revisions[i].revision != "" {
			targetRSName = revisions[i].rs
			break
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
	latest.Spec.Template = targetRS.Spec.Template
	_, err = cs.AppsV1().Deployments(namespace).Update(ctx, latest, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update deployment for rollback: %w", err)
	}

	return nil
}
