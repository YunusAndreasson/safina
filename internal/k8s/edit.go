package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// Kinds exposed to the Edit flow. The TUI uses these strings as the kind
// argument to BuildEditableYAML / ApplyEditedYAML.
const (
	EditKindDeployment  = "Deployment"
	EditKindStatefulSet = "StatefulSet"
	EditKindDaemonSet   = "DaemonSet"
	EditKindConfigMap   = "ConfigMap"
	EditKindSecret      = "Secret"
	EditKindService     = "Service"
	EditKindIngress     = "Ingress"
	EditKindPVC         = "PersistentVolumeClaim"
)

// IsEditableKind reports whether a kind can be opened for editing. Pods,
// nodes, and namespaces are intentionally excluded — the API server rejects
// most spec mutations on them, so offering edit there is a footgun. Jobs
// stay out because they're effectively immutable once created.
func IsEditableKind(kind string) bool {
	switch kind {
	case EditKindDeployment, EditKindStatefulSet, EditKindDaemonSet,
		EditKindConfigMap, EditKindSecret,
		EditKindService, EditKindIngress, EditKindPVC:
		return true
	}
	return false
}

// BuildEditableYAML fetches a single resource and returns it as YAML the user
// can edit. Strips ManagedFields (kubectl-style) and sets TypeMeta so the
// document round-trips through the editor. For Secret, Data is decoded into
// stringData so the editor shows plaintext — same reasoning as Reveal: the
// data isn't encrypted, only base64'd.
func BuildEditableYAML(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (string, error) {
	switch kind {
	case EditKindDeployment:
		obj, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "Deployment"
		return marshalYAML(obj)

	case EditKindStatefulSet:
		obj, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "StatefulSet"
		return marshalYAML(obj)

	case EditKindDaemonSet:
		obj, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "apps/v1"
		obj.Kind = "DaemonSet"
		return marshalYAML(obj)

	case EditKindConfigMap:
		obj, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "ConfigMap"
		return marshalYAML(obj)

	case EditKindSecret:
		obj, err := cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Secret"
		if len(obj.Data) > 0 {
			if obj.StringData == nil {
				obj.StringData = make(map[string]string, len(obj.Data))
			}
			for k, v := range obj.Data {
				obj.StringData[k] = string(v)
			}
			obj.Data = nil
		}
		return marshalYAML(obj)

	case EditKindService:
		obj, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "Service"
		return marshalYAML(obj)

	case EditKindIngress:
		obj, err := cs.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "networking.k8s.io/v1"
		obj.Kind = "Ingress"
		return marshalYAML(obj)

	case EditKindPVC:
		obj, err := cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		obj.ManagedFields = nil
		obj.APIVersion = "v1"
		obj.Kind = "PersistentVolumeClaim"
		return marshalYAML(obj)
	}
	return "", fmt.Errorf("edit: unsupported kind %q", kind)
}

// ApplyEditedYAML parses the edited YAML and submits it to the API server via
// Update. The original resourceVersion travels in the YAML, so the API server
// rejects the write if someone else changed the object since we read it —
// that's how we get optimistic concurrency for free without a separate diff.
//
// Sanity-checks that the embedded name/namespace match the originally-edited
// resource; renaming via the editor would otherwise create a different object
// while leaving the original untouched.
func ApplyEditedYAML(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string, data []byte) error {
	switch kind {
	case EditKindDeployment:
		var obj appsv1.Deployment
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.AppsV1().Deployments(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindStatefulSet:
		var obj appsv1.StatefulSet
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.AppsV1().StatefulSets(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindDaemonSet:
		var obj appsv1.DaemonSet
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.AppsV1().DaemonSets(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindConfigMap:
		var obj corev1.ConfigMap
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.CoreV1().ConfigMaps(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindSecret:
		var obj corev1.Secret
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		// k8s normally merges stringData into data server-side, but if the
		// user kept *both* fields, stringData wins for any conflicting key —
		// matching the API's documented precedence.
		_, err := cs.CoreV1().Secrets(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindService:
		var obj corev1.Service
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.CoreV1().Services(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindIngress:
		var obj networkingv1.Ingress
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.NetworkingV1().Ingresses(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err

	case EditKindPVC:
		var obj corev1.PersistentVolumeClaim
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("yaml parse: %w", err)
		}
		if err := assertSameTarget(obj.Namespace, obj.Name, namespace, name); err != nil {
			return err
		}
		_, err := cs.CoreV1().PersistentVolumeClaims(namespace).Update(ctx, &obj, metav1.UpdateOptions{})
		return err
	}
	return fmt.Errorf("edit: unsupported kind %q", kind)
}

func assertSameTarget(gotNS, gotName, wantNS, wantName string) error {
	if gotName != wantName || gotNS != wantNS {
		return fmt.Errorf("edit: refusing to apply — name/namespace was changed (got %s/%s, want %s/%s)",
			gotNS, gotName, wantNS, wantName)
	}
	return nil
}
