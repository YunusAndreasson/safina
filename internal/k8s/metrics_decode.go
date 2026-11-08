package k8s

import (
	"encoding/json"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// Keep the SDK response types, with a sidecar for malformed CPU/memory values.
// One invalid container must not discard valid readings from its neighbours.
type podMetricsResult struct {
	metricsv1beta1.PodMetricsList
	invalid map[types.NamespacedName]sets.Set[string]
}

func decodePodMetrics(raw []byte) (podMetricsResult, error) {
	result := podMetricsResult{invalid: make(map[types.NamespacedName]sets.Set[string])}
	wire := struct {
		*metricsv1beta1.PodMetricsList
		Items []json.RawMessage `json:"items"`
	}{PodMetricsList: &result.PodMetricsList}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return podMetricsResult{}, err
	}
	for _, item := range wire.Items {
		var pod metricsv1beta1.PodMetrics
		podWire := struct {
			*metricsv1beta1.PodMetrics
			Containers []json.RawMessage `json:"containers"`
		}{PodMetrics: &pod}
		if err := json.Unmarshal(item, &podWire); err != nil {
			return podMetricsResult{}, err
		}
		invalid := sets.New[string]()
		for _, data := range podWire.Containers {
			var container metricsv1beta1.ContainerMetrics
			containerWire := struct {
				*metricsv1beta1.ContainerMetrics
				Usage map[corev1.ResourceName]json.RawMessage `json:"usage"`
			}{ContainerMetrics: &container}
			if err := json.Unmarshal(data, &containerWire); err != nil {
				return podMetricsResult{}, err
			}
			var valid bool
			container.Usage, valid = decodeMetricResources(containerWire.Usage)
			if !valid {
				invalid.Insert(container.Name)
			}
			pod.Containers = append(pod.Containers, container)
		}
		result.Items = append(result.Items, pod)
		if invalid.Len() > 0 {
			result.invalid[types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}] = invalid
		}
	}
	return result, nil
}

type nodeMetricsResult struct {
	metricsv1beta1.NodeMetricsList
	invalid sets.Set[string]
}

func decodeNodeMetrics(raw []byte) (nodeMetricsResult, error) {
	result := nodeMetricsResult{invalid: sets.New[string]()}
	wire := struct {
		*metricsv1beta1.NodeMetricsList
		Items []json.RawMessage `json:"items"`
	}{NodeMetricsList: &result.NodeMetricsList}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nodeMetricsResult{}, err
	}
	for _, item := range wire.Items {
		var node metricsv1beta1.NodeMetrics
		nodeWire := struct {
			*metricsv1beta1.NodeMetrics
			Usage map[corev1.ResourceName]json.RawMessage `json:"usage"`
		}{NodeMetrics: &node}
		if err := json.Unmarshal(item, &nodeWire); err != nil {
			return nodeMetricsResult{}, err
		}
		var valid bool
		node.Usage, valid = decodeMetricResources(nodeWire.Usage)
		if !valid {
			result.invalid.Insert(node.Name)
		}
		result.Items = append(result.Items, node)
	}
	return result, nil
}

func decodeMetricResources(raw map[corev1.ResourceName]json.RawMessage) (corev1.ResourceList, bool) {
	usage := make(corev1.ResourceList, len(raw))
	valid := true
	for name, data := range raw {
		var q resource.Quantity
		if err := json.Unmarshal(data, &q); err != nil || string(data) == "null" {
			if name == corev1.ResourceCPU || name == corev1.ResourceMemory {
				valid = false
			}
			continue
		}
		usage[name] = q
	}
	return usage, valid
}

func metricAmounts(usage corev1.ResourceList) (cpu, memory int64, valid bool) {
	cpuQuantity, cpuOK := usage[corev1.ResourceCPU]
	memoryQuantity, memoryOK := usage[corev1.ResourceMemory]
	cpu, memory = cpuQuantity.MilliValue(), memoryQuantity.Value()
	return cpu, memory, cpuOK && memoryOK && cpuQuantity.Sign() >= 0 && memoryQuantity.Sign() >= 0 && cpu >= 0 && memory >= 0
}
