package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fixtureCheckoutWorkload returns the canonical "checkout" deployment used
// across tests: one container with envFrom (ConfigMap+Secret), a volume
// mount (Secret), a serviceAccountName, and an imagePullSecret. Lets a
// single test assert every Uses path at once.
func fixtureCheckoutDeployment(replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "shop"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "checkout", "tier": "web"}},
				Spec: corev1.PodSpec{
					ServiceAccountName: "checkout-sa",
					ImagePullSecrets:   []corev1.LocalObjectReference{{Name: "registry-pull"}},
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "ghcr.io/co/checkout:v1",
						EnvFrom: []corev1.EnvFromSource{
							{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-config"}}},
							{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "checkout-env"}}},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "tls", MountPath: "/etc/tls"}},
					}},
					Volumes: []corev1.Volume{{
						Name: "tls",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "checkout-tls"},
						},
					}, {
						Name: "data",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "checkout-data"},
						},
					}},
				},
			},
		},
	}
}

func TestConnectionsForWorkload_DeploymentSurfacesAllRefs(t *testing.T) {
	d := fixtureCheckoutDeployment(2)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api", Namespace: "shop"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "checkout"},
		},
	}
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-ing", Namespace: "shop"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: "checkout.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{Name: "checkout-api"},
							},
						}},
					},
				},
			}},
		},
	}
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-hpa", Namespace: "shop"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment", Name: "checkout",
			},
			MaxReplicas: 10,
		},
	}
	cs := fake.NewSimpleClientset(d, svc, ing, hpa)

	conns, err := ConnectionsForWorkload(context.Background(), cs, AppKindDeployment, "shop", "checkout")
	require.NoError(t, err)

	refs := refSet(conns.Uses)
	assert.Contains(t, refs, key(KindServiceAccount, "checkout-sa"), "serviceAccountName surfaces under Uses")
	assert.Contains(t, refs, key(KindSecret, "registry-pull"), "imagePullSecret surfaces under Uses")
	assert.Contains(t, refs, key(KindConfigMap, "checkout-config"), "envFrom ConfigMap surfaces under Uses")
	assert.Contains(t, refs, key(KindSecret, "checkout-env"), "envFrom Secret surfaces under Uses")
	assert.Contains(t, refs, key(KindSecret, "checkout-tls"), "volume Secret surfaces under Uses")
	assert.Contains(t, refs, key(KindPVC, "checkout-data"), "volume PVC surfaces under Uses")
	assert.Contains(t, refs, key(KindService, "checkout-api"), "Service whose selector matches surfaces under Uses")
	assert.Contains(t, refs, key(KindIngress, "checkout-ing"), "Ingress backing the matched Service surfaces under Uses")

	usedBy := refSet(conns.UsedBy)
	assert.Contains(t, usedBy, key(KindHPA, "checkout-hpa"), "HPA targeting this workload appears under UsedBy")
}

func TestConnectionsForWorkload_DedupesSameRefAcrossPaths(t *testing.T) {
	// A ConfigMap referenced via BOTH envFrom and a volume should produce
	// exactly one row — the operator doesn't need to see the same target
	// twice, and the de-dup keeps the panel readable for big workloads.
	replicas := int32(1)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dup", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": "b"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "c",
						EnvFrom: []corev1.EnvFromSource{
							{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "shared"}}},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "shared-vol",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "shared"}},
						},
					}},
				},
			},
		},
	}
	cs := fake.NewSimpleClientset(d)
	conns, err := ConnectionsForWorkload(context.Background(), cs, AppKindDeployment, "ns", "dup")
	require.NoError(t, err)
	count := 0
	for _, r := range conns.Uses {
		if r.Kind == KindConfigMap && r.Name == "shared" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate ConfigMap reference across envFrom+volume should de-dup")
}

func TestConnectionsForConfigMap_ReverseScansNamespaceOnly(t *testing.T) {
	one := int32(1)
	// Two workloads in ns "shop" reference cm "shared"; one workload in ns
	// "other" also references it. Reverse scan must respect namespace and
	// only return the two from "shop".
	mk := func(ns, name string) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: appsv1.DeploymentSpec{
				Replicas: &one,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"a": name}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name: "c",
							EnvFrom: []corev1.EnvFromSource{
								{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "shared"}}},
							},
						}},
					},
				},
			},
		}
	}
	a := mk("shop", "a")
	b := mk("shop", "b")
	c := mk("other", "c")
	cs := fake.NewSimpleClientset(a, b, c)
	conns, err := ConnectionsForConfigMap(context.Background(), cs, "shop", "shared")
	require.NoError(t, err)
	assert.Empty(t, conns.Uses, "ConfigMaps don't have a forward direction")
	usedBy := refSet(conns.UsedBy)
	assert.Len(t, usedBy, 2, "only the two in-namespace workloads should appear")
	assert.Contains(t, usedBy, key(KindApp, "a"))
	assert.Contains(t, usedBy, key(KindApp, "b"))
	for _, r := range conns.UsedBy {
		assert.Equal(t, "shop", r.Namespace, "cross-namespace workload should NOT surface")
	}
}

func TestConnectionsForService_ResolvesIngressesAndWorkloads(t *testing.T) {
	one := int32(1)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "api"}},
	}
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
			},
		},
	}
	pathType := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "api-ing", Namespace: "ns"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							PathType: &pathType,
							Backend:  networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "api"}},
						}},
					},
				},
			}},
		},
	}
	cs := fake.NewSimpleClientset(svc, d, ing)

	conns, err := ConnectionsForService(context.Background(), cs, "ns", "api")
	require.NoError(t, err)
	uses := refSet(conns.Uses)
	assert.Contains(t, uses, key(KindIngress, "api-ing"))
	usedBy := refSet(conns.UsedBy)
	assert.Contains(t, usedBy, key(KindApp, "api"), "Deployment whose template labels match the Service selector appears under UsedBy")
}

func TestConnectionsForPod_OwnerChainAndServiceSelectors(t *testing.T) {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "checkout-7d9f-xyz", Namespace: "ns",
			Labels: map[string]string{"app": "checkout"},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "checkout-7d9f", Controller: boolPtr(true)},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "checkout"}},
	}
	cs := fake.NewSimpleClientset(p, svc)
	conns, err := ConnectionsForPod(context.Background(), cs, "ns", "checkout-7d9f-xyz")
	require.NoError(t, err)
	// Owner appears under Uses with reason=ownerRef.
	found := false
	for _, r := range conns.Uses {
		if r.Name == "checkout-7d9f" && r.Reason == "ownerRef" {
			found = true
			break
		}
	}
	assert.True(t, found, "pod's controller owner should appear under Uses")

	usedBy := refSet(conns.UsedBy)
	assert.Contains(t, usedBy, key(KindService, "checkout"),
		"Services whose selector matches the pod's labels appear under UsedBy")
}

func TestConnectionsForHPA_PointsAtScaleTarget(t *testing.T) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "ns"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment", Name: "checkout",
			},
			MaxReplicas: 5,
		},
	}
	cs := fake.NewSimpleClientset(hpa)
	conns, err := ConnectionsForHPA(context.Background(), cs, "ns", "h")
	require.NoError(t, err)
	require.Len(t, conns.Uses, 1)
	assert.Equal(t, "checkout", conns.Uses[0].Name)
	assert.Equal(t, "Deployment", conns.Uses[0].SubKind)
	assert.Equal(t, "scaleTargetRef", conns.Uses[0].Reason)
}

// ── helpers ──────────────────────────────────────────────────────────

func key(kind, name string) string { return kind + "|" + name }

func refSet(refs []ConnectedRef) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, r := range refs {
		out[key(r.Kind, r.Name)] = true
	}
	return out
}
