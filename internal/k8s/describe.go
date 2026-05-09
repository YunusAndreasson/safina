package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// DescribeYAML fetches a single object identified by kind+namespace+name
// and returns it serialized as YAML — the same view `kubectl get -o yaml`
// gives. Strips ManagedFields to keep the dump readable; sets TypeMeta
// explicitly so the YAML round-trips with apiVersion/kind at the top
// (typed clients strip those on Get).
//
// Covers every kind the TUI surfaces a Describe affordance for. New
// kinds added to the tree should add a case here in the same shape so
// the footer never advertises a verb that returns "unsupported kind".
func DescribeYAML(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) (string, error) {
	switch kind {
	// ── Workloads ─────────────────────────────────────────────────
	case "Deployment":
		obj, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "Deployment"
		return marshalYAML(obj)

	case "StatefulSet":
		obj, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "StatefulSet"
		return marshalYAML(obj)

	case "DaemonSet":
		obj, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "DaemonSet"
		return marshalYAML(obj)

	case "CronJob":
		obj, err := cs.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "batch/v1"
		obj.Kind = "CronJob"
		return marshalYAML(obj)

	case "Job":
		obj, err := cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "batch/v1"
		obj.Kind = "Job"
		return marshalYAML(obj)

	case "Pod":
		obj, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Pod"
		return marshalYAML(obj)

	// ── Config / Secrets ──────────────────────────────────────────
	case "ConfigMap":
		obj, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "ConfigMap"
		return marshalYAML(obj)

	case "Secret":
		// Default Secret describe leaves Data base64-encoded — same as
		// kubectl get -o yaml. The Reveal verb (`v`) is the explicit
		// opt-in for plaintext; describe stays conservative.
		obj, err := cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Secret"
		return marshalYAML(obj)

	// ── Network ───────────────────────────────────────────────────
	case "Service":
		obj, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Service"
		return marshalYAML(obj)

	case "Ingress":
		obj, err := cs.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "networking.k8s.io/v1"
		obj.Kind = "Ingress"
		return marshalYAML(obj)

	// ── Storage ───────────────────────────────────────────────────
	case "PersistentVolumeClaim":
		obj, err := cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "PersistentVolumeClaim"
		return marshalYAML(obj)

	case "PersistentVolume":
		obj, err := cs.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "PersistentVolume"
		return marshalYAML(obj)

	// ── Cluster scope ─────────────────────────────────────────────
	case "Node":
		obj, err := cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Node"
		return marshalYAML(obj)

	case "Namespace":
		obj, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Namespace"
		return marshalYAML(obj)
	}
	return "", fmt.Errorf("describe: unsupported kind %q", kind)
}

func marshalYAML(v any) (string, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
