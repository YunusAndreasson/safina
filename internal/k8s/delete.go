package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DeleteResource removes a resource of the given kind. For workloads
// (Deployment / StatefulSet / DaemonSet / Job / CronJob) we force
// foreground cascade so the children disappear with the parent — leaving a
// half-deleted Deployment with orphan pods is never what an on-caller wants.
//
// The kind string accepts both API names ("Deployment", "PersistentVolumeClaim")
// and the friendlier tree-internal names ("PVC", "Pod"). actionTarget in the
// TUI layer already normalises to API names; this dispatcher tolerates either
// so callers don't have to remember which path they're on.
func DeleteResource(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) error {
	fg := metav1.DeletePropagationForeground
	cascade := metav1.DeleteOptions{PropagationPolicy: &fg}
	bare := metav1.DeleteOptions{}

	switch kind {
	case KindPod:
		return cs.CoreV1().Pods(namespace).Delete(ctx, name, bare)
	case AppKindDeployment:
		return cs.AppsV1().Deployments(namespace).Delete(ctx, name, cascade)
	case AppKindStatefulSet:
		return cs.AppsV1().StatefulSets(namespace).Delete(ctx, name, cascade)
	case AppKindDaemonSet:
		return cs.AppsV1().DaemonSets(namespace).Delete(ctx, name, cascade)
	case AppKindJob:
		return cs.BatchV1().Jobs(namespace).Delete(ctx, name, cascade)
	case AppKindCronJob:
		return cs.BatchV1().CronJobs(namespace).Delete(ctx, name, cascade)
	case KindConfigMap:
		return cs.CoreV1().ConfigMaps(namespace).Delete(ctx, name, bare)
	case KindSecret:
		return cs.CoreV1().Secrets(namespace).Delete(ctx, name, bare)
	case KindService:
		return cs.CoreV1().Services(namespace).Delete(ctx, name, bare)
	case KindIngress:
		return cs.NetworkingV1().Ingresses(namespace).Delete(ctx, name, bare)
	case "PersistentVolumeClaim", KindPVC:
		return cs.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, name, bare)
	}
	return fmt.Errorf("delete not supported for kind %q", kind)
}
