package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ListConfigMapsAcrossNamespaces lists ConfigMaps across the given
// namespaces, returning a flat slice sorted by namespace then name.
func ListConfigMapsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]ConfigMapInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	var (
		mu  sync.Mutex
		out []ConfigMapInfo
		wg  sync.WaitGroup
	)
	for _, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]ConfigMapInfo, 0, len(list.Items))
			for _, cm := range list.Items {
				rows = append(rows, ConfigMapInfo{
					Name:      cm.Name,
					Namespace: cm.Namespace,
					Keys:      len(cm.Data) + len(cm.BinaryData),
					Age:       HumanAge(cm.CreationTimestamp.Time),
				})
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListServicesAcrossNamespaces lists Services across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListServicesAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]ServiceInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	var (
		mu  sync.Mutex
		out []ServiceInfo
		wg  sync.WaitGroup
	)
	for _, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]ServiceInfo, 0, len(list.Items))
			for _, s := range list.Items {
				ports := make([]string, len(s.Spec.Ports))
				for i, p := range s.Spec.Ports {
					ports[i] = fmt.Sprintf("%d/%s", p.Port, p.Protocol)
				}
				rows = append(rows, ServiceInfo{
					Name:      s.Name,
					Namespace: s.Namespace,
					Type:      string(s.Spec.Type),
					ClusterIP: s.Spec.ClusterIP,
					Ports:     strings.Join(ports, ", "),
					Age:       HumanAge(s.CreationTimestamp.Time),
				})
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListIngressesAcrossNamespaces lists Ingresses across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListIngressesAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]IngressInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	var (
		mu  sync.Mutex
		out []IngressInfo
		wg  sync.WaitGroup
	)
	for _, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]IngressInfo, 0, len(list.Items))
			for _, ing := range list.Items {
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
				rows = append(rows, IngressInfo{
					Name:      ing.Name,
					Namespace: ing.Namespace,
					Hosts:     strings.Join(hosts, ", "),
					Class:     class,
					Age:       HumanAge(ing.CreationTimestamp.Time),
				})
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
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
		// Check if any rule references this service.
		found := false
		for _, r := range ing.Spec.Rules {
			for _, p := range r.HTTP.Paths {
				if p.Backend.Service != nil && p.Backend.Service.Name == serviceName {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			// Also check the default backend.
			if ing.Spec.DefaultBackend != nil &&
				ing.Spec.DefaultBackend.Service != nil &&
				ing.Spec.DefaultBackend.Service.Name == serviceName {
				found = true
			}
		}
		if !found {
			continue
		}
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
		out = append(out, IngressInfo{
			Name:      ing.Name,
			Namespace: ing.Namespace,
			Hosts:     strings.Join(hosts, ", "),
			Class:     class,
			Age:       HumanAge(ing.CreationTimestamp.Time),
		})
	}
	return out, nil
}

// ListSecretsAcrossNamespaces lists Secrets across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListSecretsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]SecretInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	var (
		mu  sync.Mutex
		out []SecretInfo
		wg  sync.WaitGroup
	)
	for _, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]SecretInfo, 0, len(list.Items))
			for _, s := range list.Items {
				rows = append(rows, SecretInfo{
					Name:      s.Name,
					Namespace: s.Namespace,
					Type:      string(s.Type),
					Keys:      len(s.Data),
					Age:       HumanAge(s.CreationTimestamp.Time),
				})
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ListPVCsAcrossNamespaces lists PVCs across the given namespaces,
// returning a flat slice sorted by namespace then name.
func ListPVCsAcrossNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]PVCInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}
	var (
		mu  sync.Mutex
		out []PVCInfo
		wg  sync.WaitGroup
	)
	for _, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]PVCInfo, 0, len(list.Items))
			for _, pvc := range list.Items {
				capacity := ""
				if q, ok := pvc.Spec.Resources.Requests["storage"]; ok {
					capacity = q.String()
				}
				sc := ""
				if pvc.Spec.StorageClassName != nil {
					sc = *pvc.Spec.StorageClassName
				}
				rows = append(rows, PVCInfo{
					Name:         pvc.Name,
					Namespace:    pvc.Namespace,
					Status:       string(pvc.Status.Phase),
					Capacity:     capacity,
					VolumeName:   pvc.Spec.VolumeName,
					StorageClass: sc,
					Age:          HumanAge(pvc.CreationTimestamp.Time),
				})
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
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
	if len(s.Data) > 0 {
		if s.StringData == nil {
			s.StringData = make(map[string]string, len(s.Data))
		}
		for k, v := range s.Data {
			s.StringData[k] = string(v)
		}
		s.Data = nil
	}
	return marshalYAML(s)
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
