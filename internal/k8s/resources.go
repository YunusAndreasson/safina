package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/yunus/safina/internal/debug"
)

// ListConfigMapsAcrossNamespaces lists ConfigMaps across the given
// namespaces, returning a flat slice sorted by namespace then name.
func ListConfigMapsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]ConfigMapInfo, error) {
	out, err := listNamespaceRows(ctx, namespaces, "ConfigMap", func(ctx context.Context, ns string) ([]corev1.ConfigMap, error) {
		list, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}, configMapToInfo)
	sortByNSName(out, func(r ConfigMapInfo) (string, string) { return r.Namespace, r.Name })
	return out, err
}

// ListServicesAcrossNamespaces lists Services across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListServicesAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]ServiceInfo, error) {
	out, err := listNamespaceRows(ctx, namespaces, "Service", func(ctx context.Context, ns string) ([]corev1.Service, error) {
		list, err := cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}, serviceToInfo)
	sortByNSName(out, func(r ServiceInfo) (string, string) { return r.Namespace, r.Name })
	return out, err
}

// ListIngressesAcrossNamespaces lists Ingresses across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListIngressesAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]IngressInfo, error) {
	out, err := listNamespaceRows(ctx, namespaces, "Ingress", func(ctx context.Context, ns string) ([]networkingv1.Ingress, error) {
		list, err := cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}, ingressToInfo)
	sortByNSName(out, func(r IngressInfo) (string, string) { return r.Namespace, r.Name })
	return out, err
}

// ListIngressesForService filters Ingresses to those that reference the
// given service name (via backend). Used when drilling into a Service.
func ListIngressesForService(ctx context.Context, cs kubernetes.Interface, namespace, serviceName string) ([]IngressInfo, error) {
	list, err := cs.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []IngressInfo
	for _, ing := range list.Items {
		if !ingressReferencesService(ing, serviceName) {
			continue
		}
		out = append(out, ingressToInfo(ing))
	}
	return out, nil
}

// ingressReferencesService reports whether ing routes to serviceName via any
// rule backend or the default backend.
func ingressReferencesService(ing networkingv1.Ingress, serviceName string) bool {
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil && p.Backend.Service.Name == serviceName {
				return true
			}
		}
	}
	return ing.Spec.DefaultBackend != nil &&
		ing.Spec.DefaultBackend.Service != nil &&
		ing.Spec.DefaultBackend.Service.Name == serviceName
}

// ListSecretsAcrossNamespaces lists Secrets across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListSecretsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]SecretInfo, error) {
	out, err := listNamespaceRows(ctx, namespaces, "Secret", func(ctx context.Context, ns string) ([]corev1.Secret, error) {
		list, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}, secretToInfo)
	sortByNSName(out, func(r SecretInfo) (string, string) { return r.Namespace, r.Name })
	return out, err
}

// ListPVCsAcrossNamespaces lists PVCs across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListPVCsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]PVCInfo, error) {
	out, err := listNamespaceRows(ctx, namespaces, "PVC", func(ctx context.Context, ns string) ([]corev1.PersistentVolumeClaim, error) {
		list, err := cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}, pvcToInfo)
	sortByNSName(out, func(r PVCInfo) (string, string) { return r.Namespace, r.Name })
	return out, err
}

// RevealSecretYAML fetches a Secret and serializes it with Data decoded into
// stringData. The result is plain YAML — same shape as `kubectl get secret -o
// yaml` would produce, but with the values readable instead of base64. Safe
// for the TUI to display because k8s Secrets are not encrypted at the API
// layer; anyone with read access already has the bytes.
func RevealSecretYAML(ctx context.Context, cs kubernetes.Interface, namespace, name string) (string, error) {
	s, err := cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	s.ManagedFields = nil
	s.APIVersion = "v1"
	s.Kind = "Secret"
	decodeSecretText(s)
	return marshalYAML(s)
}

// ── Row converters ───────────────────────────────────────────────────
//
// One converter per kind, shared between the direct-API fan-outs above and
// the cache-aware Client methods (client_cache.go). All read-only: the cache
// path hands in SHARED informer objects.

func configMapToInfo(cm corev1.ConfigMap) ConfigMapInfo {
	return ConfigMapInfo{
		Name:      cm.Name,
		Namespace: cm.Namespace,
		Keys:      len(cm.Data) + len(cm.BinaryData),
		KeyNames:  sortedDataKeyNames(cm.Data, cm.BinaryData),
		Age:       HumanAge(cm.CreationTimestamp.Time),
	}
}

func serviceToInfo(s corev1.Service) ServiceInfo {
	ports := make([]string, len(s.Spec.Ports))
	for i, p := range s.Spec.Ports {
		ports[i] = fmt.Sprintf("%d/%s", p.Port, p.Protocol)
	}
	return ServiceInfo{
		Name:      s.Name,
		Namespace: s.Namespace,
		Type:      string(s.Spec.Type),
		ClusterIP: s.Spec.ClusterIP,
		Ports:     strings.Join(ports, ", "),
		Age:       HumanAge(s.CreationTimestamp.Time),
	}
}

func ingressToInfo(ing networkingv1.Ingress) IngressInfo {
	hosts := make([]string, 0, len(ing.Spec.Rules))
	for _, r := range ing.Spec.Rules {
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
	}
	class := ""
	if ing.Spec.IngressClassName != nil {
		class = *ing.Spec.IngressClassName
	}
	return IngressInfo{
		Name:      ing.Name,
		Namespace: ing.Namespace,
		Hosts:     strings.Join(hosts, ", "),
		Class:     class,
		Age:       HumanAge(ing.CreationTimestamp.Time),
	}
}

func secretToInfo(s corev1.Secret) SecretInfo {
	return SecretInfo{
		Name:      s.Name,
		Namespace: s.Namespace,
		Type:      string(s.Type),
		Keys:      len(s.Data),
		KeyNames:  sortedSecretKeyNames(s.Data),
		Age:       HumanAge(s.CreationTimestamp.Time),
	}
}

func pvcToInfo(pvc corev1.PersistentVolumeClaim) PVCInfo {
	capacity := ""
	if q, ok := pvc.Spec.Resources.Requests["storage"]; ok {
		capacity = q.String()
	}
	sc := ""
	if pvc.Spec.StorageClassName != nil {
		sc = *pvc.Spec.StorageClassName
	}
	return PVCInfo{
		Name:         pvc.Name,
		Namespace:    pvc.Namespace,
		Status:       string(pvc.Status.Phase),
		Capacity:     capacity,
		VolumeName:   pvc.Spec.VolumeName,
		StorageClass: sc,
		Age:          HumanAge(pvc.CreationTimestamp.Time),
	}
}

// sortedDataKeyNames returns a sorted union of map keys from a ConfigMap's
// Data and BinaryData maps. Used by the inspector to preview a configmap's
// schema without opening the reveal modal.
func sortedDataKeyNames(data map[string]string, binary map[string][]byte) []string {
	if len(data) == 0 && len(binary) == 0 {
		return nil
	}
	out := make([]string, 0, len(data)+len(binary))
	for k := range data {
		out = append(out, k)
	}
	for k := range binary {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedSecretKeyNames returns sorted key names of a Secret's data map.
// Names only — never values. The reveal modal is the canonical view for
// values; the inspector just shows "what schema is this?".
func sortedSecretKeyNames(data map[string][]byte) []string {
	if len(data) == 0 {
		return nil
	}
	out := make([]string, 0, len(data))
	for k := range data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GetPVByName returns a single PV by name. Used when drilling into a PVC
// to show its bound volume.
func GetPVByName(ctx context.Context, cs kubernetes.Interface, name string) (*PVInfo, error) {
	if name == "" {
		return nil, nil
	}
	pv, err := cs.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	capacity := ""
	if q, ok := pv.Spec.Capacity["storage"]; ok {
		capacity = q.String()
	}
	claim := ""
	if pv.Spec.ClaimRef != nil {
		claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
	}
	modes := make([]string, len(pv.Spec.AccessModes))
	for i, m := range pv.Spec.AccessModes {
		modes[i] = string(m)
	}
	return &PVInfo{
		Name:         pv.Name,
		Capacity:     capacity,
		Status:       string(pv.Status.Phase),
		Claim:        claim,
		StorageClass: pv.Spec.StorageClassName,
		AccessModes:  strings.Join(modes, ","),
		Reclaim:      string(pv.Spec.PersistentVolumeReclaimPolicy),
		Age:          HumanAge(pv.CreationTimestamp.Time),
	}, nil
}

// namespaceListConcurrency bounds outstanding API calls even on clusters with
// thousands of namespaces. The worker pool also bounds goroutine allocation.
const namespaceListConcurrency = 8

// listNamespaceRows preserves accessible namespaces on partial RBAC failures,
// but reports complete failures instead of displaying a misleading empty list.
func listNamespaceRows[T, R any](ctx context.Context, namespaces []string, kind string,
	list func(context.Context, string) ([]T, error), convert func(T) R,
) ([]R, error) {
	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		out       []R
		failures  []error
		successes int
	)
	jobs := make(chan string)
	for range min(namespaceListConcurrency, len(namespaces)) {
		wg.Go(func() {
			for ns := range jobs {
				if ctx.Err() != nil {
					continue
				}
				items, err := list(ctx, ns)
				if err != nil {
					debug.Errf("k8s.list_partial", "kind", kind, "namespace", ns, "err", err.Error())
					mu.Lock()
					failures = append(failures, fmt.Errorf("%s in namespace %s: %w", kind, ns, err))
					mu.Unlock()
					continue
				}
				rows := make([]R, len(items))
				for i, item := range items {
					rows[i] = convert(item)
				}
				mu.Lock()
				successes++
				out = append(out, rows...)
				mu.Unlock()
			}
		})
	}
enqueue:
	for _, ns := range namespaces {
		select {
		case jobs <- ns:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if successes == 0 {
		return nil, errors.Join(failures...)
	}
	return out, nil
}

// decodeSecretText makes text editable without corrupting binary keys through
// JSON's replacement of invalid UTF-8. Binary values remain base64 in data.
// Call only on a fresh API object, never on a shared informer-cache object.
func decodeSecretText(secret *corev1.Secret) {
	for key, value := range secret.Data {
		if !utf8.Valid(value) {
			continue
		}
		if secret.StringData == nil {
			secret.StringData = make(map[string]string)
		}
		if _, exists := secret.StringData[key]; !exists {
			secret.StringData[key] = string(value)
		}
		delete(secret.Data, key)
	}
}
