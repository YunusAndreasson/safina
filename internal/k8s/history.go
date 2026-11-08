package k8s

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/yunus/safina/internal/history"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *Manager) HistoryTarget(contextName, namespace, kind, name string) history.Target {
	t := history.Target{Context: contextName, Namespace: namespace, Kind: kind, Name: name}
	if c, ok := m.GetClient(contextName); ok && c.RESTConfig != nil {
		ca := c.RESTConfig.CAData
		if len(ca) == 0 && c.RESTConfig.CAFile != "" {
			ca, _ = os.ReadFile(c.RESTConfig.CAFile)
		}
		t.Cluster = history.ClusterID(c.RESTConfig.Host, ca)
	} else {
		t.Cluster = history.ClusterID(contextName, nil)
	}
	return t
}

// HistorySnapshot deliberately selects fields; full manifests and arbitrary
// annotations, environment values, and Secret data never enter the journal.
func (c *Client) HistorySnapshot(ctx context.Context, t history.Target) (history.Target, map[string]string) {
	fields := map[string]string{}
	var obj metav1.Object
	switch t.Kind {
	case "Deployment":
		d, err := c.Clientset.AppsV1().Deployments(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
		if err != nil {
			return t, fields
		}
		obj = d
		fields["revision"] = d.Annotations["deployment.kubernetes.io/revision"]
		fields["ready"] = strconv.Itoa(int(d.Status.AvailableReplicas))
		for _, condition := range d.Status.Conditions {
			fields["condition."+string(condition.Type)] = string(condition.Status) + "/" + condition.Reason
		}
		if d.Spec.Replicas != nil {
			fields["replicas"] = strconv.Itoa(int(*d.Spec.Replicas))
		}
		for _, v := range d.Spec.Template.Spec.Containers {
			fields["image."+v.Name] = v.Image
		}
	case "StatefulSet":
		d, err := c.Clientset.AppsV1().StatefulSets(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
		if err != nil {
			return t, fields
		}
		obj = d
		fields["revision"] = d.Status.UpdateRevision
		fields["ready"] = strconv.Itoa(int(d.Status.ReadyReplicas))
		for _, condition := range d.Status.Conditions {
			fields["condition."+string(condition.Type)] = string(condition.Status) + "/" + condition.Reason
		}
		if d.Spec.Replicas != nil {
			fields["replicas"] = strconv.Itoa(int(*d.Spec.Replicas))
		}
		for _, v := range d.Spec.Template.Spec.Containers {
			fields["image."+v.Name] = v.Image
		}
	case "DaemonSet":
		d, err := c.Clientset.AppsV1().DaemonSets(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
		if err != nil {
			return t, fields
		}
		obj = d
		fields["ready"] = strconv.Itoa(int(d.Status.NumberReady))
		for _, condition := range d.Status.Conditions {
			fields["condition."+string(condition.Type)] = string(condition.Status) + "/" + condition.Reason
		}
		for _, v := range d.Spec.Template.Spec.Containers {
			fields["image."+v.Name] = v.Image
		}
	case "Pod":
		d, err := c.Clientset.CoreV1().Pods(t.Namespace).Get(ctx, t.Name, metav1.GetOptions{})
		if err != nil {
			return t, fields
		}
		obj = d
		fields["phase"] = string(d.Status.Phase)
		for _, owner := range d.OwnerReferences {
			if owner.Controller == nil || !*owner.Controller {
				continue
			}
			t.WorkloadKind = owner.Kind
			t.WorkloadName = owner.Name
			t.WorkloadUID = owner.UID
			if owner.Kind == "ReplicaSet" {
				if rs, e := c.Clientset.AppsV1().ReplicaSets(t.Namespace).Get(ctx, owner.Name, metav1.GetOptions{}); e == nil && rs.UID == owner.UID {
					for _, parent := range rs.OwnerReferences {
						if parent.Controller != nil && *parent.Controller {
							t.WorkloadKind = parent.Kind
							t.WorkloadName = parent.Name
							t.WorkloadUID = parent.UID
							break
						}
					}
				}
			}
			break
		}
	default:
		return t, fields
	}
	t.UID = obj.GetUID()
	fields["generation"] = strconv.FormatInt(obj.GetGeneration(), 10)
	fields["resourceVersion"] = obj.GetResourceVersion()
	return t, fields
}

// SnapshotContext bounds optional evidence reads independently of normal API timeouts.
func SnapshotContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 2*time.Second)
}

// RecordMutationResponse uses the server response, never a later unrelated GET,
// to bind readiness monitoring to the exact resource version accepted.
func RecordMutationResponse(ctx context.Context, obj metav1.Object, err error) {
	if err != nil {
		return
	}
	op := history.FromContext(ctx)
	if op == nil {
		return
	}
	op.Target.UID = obj.GetUID()
	op.ResourceVersion = obj.GetResourceVersion()
	if obj.GetGeneration() > 0 {
		op.Generation = strconv.FormatInt(obj.GetGeneration(), 10)
	}
	op.Record("server_response", "", map[string]string{"generation": op.Generation, "resourceVersion": op.ResourceVersion})
}

// RecordEditPaths compares transient manifests but saves paths only, never values.
func (m *Manager) RecordEditPaths(ctx context.Context, t history.Target, data []byte) {
	op := history.FromContext(ctx)
	if op == nil || !m.History.EvidenceEnabled() {
		return
	}
	c, ok := m.GetClient(t.Context)
	if !ok {
		return
	}
	readCtx, cancel := SnapshotContext(ctx)
	defer cancel()
	before, err := BuildEditableYAML(readCtx, c.Clientset, t.Kind, t.Namespace, t.Name)
	if err != nil {
		op.Record("evidence_unavailable", "edit field comparison unavailable", nil)
		return
	}
	var left, right map[string]any
	if yaml.Unmarshal([]byte(before), &left) != nil || yaml.Unmarshal(data, &right) != nil {
		return
	}
	paths := make([]string, 0)
	var compare func(string, any, any)
	compare = func(path string, a, b any) {
		if len(paths) >= 256 || reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := make([]string, 0, len(am)+len(bm))
			for k := range am {
				keys = append(keys, k)
			}
			for k := range bm {
				if _, ok := am[k]; !ok {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			for _, k := range keys {
				compare(path+"/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1"), am[k], bm[k])
			}
			return
		}
		paths = append(paths, path)
	}
	compare("", left, right)
	b, err := json.Marshal(paths)
	if err == nil {
		op.Record("edit_fields", "paths only; arrays reported as a whole; maximum 256 paths", map[string]string{"paths": string(b)})
	}
}
