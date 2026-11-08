package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestInspectPod_ProjectsKeyFields(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "p1",
			Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "p1-7d9f", Controller: boolPtr(true)},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "main",
				Image: "ghcr.io/co/checkout:v1.2.3",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("64Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase:    corev1.PodRunning,
			QOSClass: corev1.PodQOSBurstable,
			PodIP:    "10.4.0.12",
			HostIP:   "10.0.0.5",
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "main",
				Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	})

	d, err := InspectPod(context.Background(), cs, "ns", "p1")
	require.NoError(t, err)
	assert.Equal(t, "10.4.0.12", d.PodIP)
	assert.Equal(t, "10.0.0.5", d.HostIP)
	assert.Equal(t, corev1.PodQOSBurstable, d.QoS)
	assert.Equal(t, "ReplicaSet", d.OwnerKind)
	assert.Equal(t, "p1-7d9f", d.OwnerName)
	require.Len(t, d.Containers, 1)
	assert.Equal(t, "ghcr.io/co/checkout:v1.2.3", d.Containers[0].Image)
	assert.Equal(t, "Running", d.Containers[0].State)
	assert.True(t, d.Containers[0].Ready)
	// Resources roll up across containers (one container here).
	assert.Equal(t, int64(100), d.CPURequestMilli)
	assert.Equal(t, int64(500), d.CPULimitMilli)
	assert.Equal(t, int64(64*1024*1024), d.MemRequestBytes)
	assert.Equal(t, int64(256*1024*1024), d.MemLimitBytes)
}

func TestInspectPod_PendingSurfacesSchedulingReason(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p2", Namespace: "ns"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  "Unschedulable",
				Message: "0/3 nodes are available: 3 Insufficient memory.",
			}},
		},
	})
	d, err := InspectPod(context.Background(), cs, "ns", "p2")
	require.NoError(t, err)
	assert.Contains(t, d.SchedulingReason, "Unschedulable")
	assert.Contains(t, d.SchedulingReason, "Insufficient memory")
}

func TestInspectApp_DeploymentImagesAndConditions(t *testing.T) {
	one := int32(3)
	cs := fake.NewSimpleClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout", Namespace: "ns", Generation: 5},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "checkout"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "main", Image: "ghcr.io/co/checkout:v1.2.3"},
					{Name: "sidecar", Image: "ghcr.io/co/sidecar:latest"},
				},
			}},
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 5,
			UpdatedReplicas:    3, ReadyReplicas: 3,
			Conditions: []appsv1.DeploymentCondition{
				{Type: "Available", Status: corev1.ConditionTrue, Reason: "MinimumReplicasAvailable"},
				{Type: "Progressing", Status: corev1.ConditionTrue, Reason: "NewReplicaSetAvailable"},
			},
		},
	})

	d, err := InspectApp(context.Background(), cs, AppKindDeployment, "ns", "checkout")
	require.NoError(t, err)
	assert.Equal(t, AppKindDeployment, d.Kind)
	assert.ElementsMatch(t, []string{
		"ghcr.io/co/checkout:v1.2.3",
		"ghcr.io/co/sidecar:latest",
	}, d.Images)
	assert.Equal(t, "RollingUpdate", d.Strategy)
	assert.Equal(t, "app=checkout", d.Selector)
	assert.Equal(t, int32(3), d.DesiredReplicas)
	assert.Equal(t, int32(3), d.ReadyReplicas)
	assert.Len(t, d.Conditions, 2)
}

func TestInspectIngress_TLSAndRules(t *testing.T) {
	cs := fake.NewSimpleClientset(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "ns"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "a.example.com"}, {Host: "b.example.com"}},
			TLS:   []networkingv1.IngressTLS{{Hosts: []string{"a.example.com", "b.example.com"}}},
		},
	})
	d, err := InspectIngress(context.Background(), cs, "ns", "web")
	require.NoError(t, err)
	assert.Equal(t, 2, d.Rules)
	assert.ElementsMatch(t, []string{"a.example.com", "b.example.com"}, d.TLSHosts)
}

func TestInspectService_EndpointCount(t *testing.T) {
	tr := true
	fa := false
	cs := fake.NewSimpleClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "ns"},
			Spec: corev1.ServiceSpec{
				ClusterIP: "10.0.0.1",
				Selector:  map[string]string{"app": "api"},
			},
		},
		&discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name: "api-abc", Namespace: "ns",
				Labels: map[string]string{discoveryv1.LabelServiceName: "api"},
			},
			Endpoints: []discoveryv1.Endpoint{
				{Addresses: []string{"10.4.0.1"}, Conditions: discoveryv1.EndpointConditions{Ready: &tr}},
				{Addresses: []string{"10.4.0.2"}, Conditions: discoveryv1.EndpointConditions{Ready: &tr}},
				{Addresses: []string{"10.4.0.3"}, Conditions: discoveryv1.EndpointConditions{Ready: &fa}},
			},
		},
	)
	d, err := InspectService(context.Background(), cs, "ns", "api")
	require.NoError(t, err)
	assert.Equal(t, 2, d.ReadyEndpointCount)
	assert.Equal(t, 3, d.EndpointCount)
	assert.Equal(t, "app=api", d.Selector)
	assert.False(t, d.HeadlessClusterIP)
}

func TestInspectNode_TaintsAndPressure(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: "dedicated", Value: "ingress", Effect: corev1.TaintEffectNoSchedule}},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{
				KernelVersion:           "5.15.0",
				OSImage:                 "Ubuntu 22.04",
				ContainerRuntimeVersion: "containerd://1.7.0",
				KubeletVersion:          "v1.29.0",
				Architecture:            "amd64",
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue, Reason: "KubeletHasInsufficientMemory"},
				{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
			},
		},
	})
	d, err := InspectNode(context.Background(), cs, "n1")
	require.NoError(t, err)
	assert.Equal(t, "5.15.0", d.KernelVersion)
	assert.Equal(t, "Ubuntu 22.04", d.OSImage)
	require.Len(t, d.Taints, 1)
	assert.Equal(t, corev1.TaintEffectNoSchedule, d.Taints[0].Effect)
	// Only abnormal non-Ready conditions whose Status is True surface.
	require.Len(t, d.Conditions, 1)
	assert.Equal(t, corev1.NodeMemoryPressure, d.Conditions[0].Type)
}

func TestInspectIngressPreservesNamedAndNumericBackendPorts(t *testing.T) {
	named := networkingv1.ServiceBackendPort{Name: "http"}
	numeric := networkingv1.ServiceBackendPort{Number: 8080}
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: "shop.example.com", IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/", Backend: networkingv1.IngressBackend{
					Service: &networkingv1.IngressServiceBackend{Name: "web", Port: named},
				}}}},
			}}},
			DefaultBackend: &networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "fallback", Port: numeric}},
		},
	}
	client := fake.NewSimpleClientset(ingress)
	detail, err := InspectIngress(t.Context(), client, "shop", "web")
	require.NoError(t, err)
	require.Len(t, detail.Backends, 2)
	require.Equal(t, named, detail.Backends[0].Port)
	require.Equal(t, numeric, detail.Backends[1].Port)
}

func TestSelectorFormattingRetainsMatchExpressions(t *testing.T) {
	selector := &metav1.LabelSelector{
		MatchLabels:      map[string]string{"app": "web"},
		MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend", "edge"}}},
	}
	require.Equal(t, "app=web,tier in (edge,frontend)", formatLabelSelector(selector))
	require.Empty(t, formatLabelSelector(nil))
	require.Empty(t, formatLabelSelector(&metav1.LabelSelector{}))
	selector.MatchExpressions[0].Operator = "unsupported"
	require.Equal(t, "<invalid selector>", formatLabelSelector(selector))
}
