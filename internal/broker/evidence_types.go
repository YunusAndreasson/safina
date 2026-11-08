package broker

import (
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Evidence projections keep the selected-field boundary explicit: adding an
// SDK field to an API object must not automatically expose it to investigations.
type workloadEvidence struct {
	Target          inv.Target              `json:"target"`
	ResourceVersion string                  `json:"resourceVersion"`
	Generation      int64                   `json:"generation"`
	Replicas        *int32                  `json:"replicas"`
	Status          appsv1.DeploymentStatus `json:"status"`
	Containers      []containerEvidence     `json:"containers"`
	// Nil omits related lists when no selector was usable. A pointer to a nil
	// slice preserves the explicit null used for an empty related list.
	ReplicaSets *[]replicaSetEvidence `json:"replicaSets,omitempty"`
	Pods        *[]podEvidence        `json:"pods,omitempty"`
}

type containerEvidence struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type replicaSetEvidence struct {
	Name          string    `json:"name"`
	UID           types.UID `json:"uid"`
	Revision      string    `json:"revision"`
	Replicas      *int32    `json:"replicas"`
	ReadyReplicas int32     `json:"readyReplicas"`
}

type podEvidence struct {
	Name              string                   `json:"name"`
	UID               types.UID                `json:"uid"`
	Phase             corev1.PodPhase          `json:"phase"`
	Conditions        []corev1.PodCondition    `json:"conditions"`
	ContainerStatuses []corev1.ContainerStatus `json:"containerStatuses"`
}

type eventEvidence struct {
	Reason         string           `json:"reason"`
	Message        string           `json:"message"`
	Type           string           `json:"type"`
	Count          int32            `json:"count"`
	FirstTimestamp metav1.Time      `json:"firstTimestamp"`
	LastTimestamp  metav1.Time      `json:"lastTimestamp"`
	EventTime      metav1.MicroTime `json:"eventTime"`
}
