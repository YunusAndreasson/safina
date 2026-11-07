package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
	yamlv3 "sigs.k8s.io/yaml/goyaml.v3"
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
		decodeSecretText(obj)
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
		return applyEditedObject(ctx, data, &appsv1.Deployment{}, namespace, name, cs.AppsV1().Deployments(namespace).Update)
	case EditKindStatefulSet:
		return applyEditedObject(ctx, data, &appsv1.StatefulSet{}, namespace, name, cs.AppsV1().StatefulSets(namespace).Update)
	case EditKindDaemonSet:
		return applyEditedObject(ctx, data, &appsv1.DaemonSet{}, namespace, name, cs.AppsV1().DaemonSets(namespace).Update)
	case EditKindConfigMap:
		return applyEditedObject(ctx, data, &corev1.ConfigMap{}, namespace, name, cs.CoreV1().ConfigMaps(namespace).Update)
	case EditKindSecret:
		return applyEditedObject(ctx, data, &corev1.Secret{}, namespace, name, cs.CoreV1().Secrets(namespace).Update)
	case EditKindService:
		return applyEditedObject(ctx, data, &corev1.Service{}, namespace, name, cs.CoreV1().Services(namespace).Update)
	case EditKindIngress:
		return applyEditedObject(ctx, data, &networkingv1.Ingress{}, namespace, name, cs.NetworkingV1().Ingresses(namespace).Update)
	case EditKindPVC:
		return applyEditedObject(ctx, data, &corev1.PersistentVolumeClaim{}, namespace, name, cs.CoreV1().PersistentVolumeClaims(namespace).Update)
	}
	return fmt.Errorf("edit: unsupported kind %q", kind)
}

// applyEditedObject keeps validation identical for every supported typed client.
// Strict decoding rejects typos and duplicate keys instead of silently dropping
// fields from the full-object Update sent to the API server.
func applyEditedObject[T metav1.Object](ctx context.Context, data []byte, obj T, namespace, name string,
	update func(context.Context, T, metav1.UpdateOptions) (T, error),
) error {
	// UnmarshalStrict only reads the first YAML document. Reject a stream
	// instead of reporting success after silently discarding later edits.
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	var document yamlv3.Node
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("yaml parse: %w", err)
	}
	if err := decoder.Decode(&yamlv3.Node{}); !errors.Is(err, io.EOF) {
		return errors.New("yaml parse: expected a single resource document")
	}
	if err := yaml.UnmarshalStrict(data, obj); err != nil {
		return fmt.Errorf("yaml parse: %w", err)
	}
	if err := assertSameTarget(obj.GetNamespace(), obj.GetName(), namespace, name); err != nil {
		return err
	}
	// Some API resources allow unconditional updates without a version.
	// An editor save must retain the version read to avoid lost updates.
	if obj.GetResourceVersion() == "" {
		return errors.New("edit: refusing to apply — metadata.resourceVersion must be retained")
	}
	result, err := update(ctx, obj, metav1.UpdateOptions{})
	RecordMutationResponse(ctx, result, err)
	return err
}

func assertSameTarget(gotNS, gotName, wantNS, wantName string) error {
	if gotName != wantName || gotNS != wantNS {
		return fmt.Errorf("edit: refusing to apply — name/namespace was changed (got %s/%s, want %s/%s)",
			gotNS, gotName, wantNS, wantName)
	}
	return nil
}
