package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// DescribeYAML fetches a single object identified by kind+namespace+name and
// returns it serialized as YAML — the same view kubectl get -o yaml gives.
//
// Currently supports Deployment and Pod. The YAML is server-side state,
// including spec + status; we strip ManagedFields to keep the dump readable.
func DescribeYAML(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) (string, error) {
	switch kind {
	case "Deployment":
		obj, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		// Set TypeMeta explicitly — the typed clients strip it, but YAML is
		// expected to round-trip with apiVersion/kind at the top.
		obj.APIVersion = "apps/v1"
		obj.Kind = "Deployment"
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

	default:
		return "", fmt.Errorf("describe: unsupported kind %q", kind)
	}
}

func marshalYAML(v any) (string, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
