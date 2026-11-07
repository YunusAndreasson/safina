package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/yunus/safina/internal/history"
	inv "github.com/yunus/safina/internal/investigation"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

func (s *server) redactText(text string) string {
	redact, err := history.NewRedactor(s.opts.History.RedactPatterns)
	if err != nil {
		return "[redaction unavailable]"
	}
	return redact(text)
}

func ownedRevision(ctx context.Context, cs kubernetes.Interface, d *appsv1.Deployment, revision int64) (*appsv1.ReplicaSet, error) {
	options := metav1.ListOptions{Limit: 500}
	if d.Spec.Selector != nil {
		selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
		if err != nil {
			return nil, err
		}
		options.LabelSelector = selector.String()
	}
	list, err := cs.AppsV1().ReplicaSets(d.Namespace).List(ctx, options)
	if err != nil {
		return nil, err
	}
	if list.Continue != "" {
		return nil, inv.ErrConflict
	}
	var found *appsv1.ReplicaSet
	for i := range list.Items {
		rs := &list.Items[i]
		if rs.Annotations["deployment.kubernetes.io/revision"] != strconv.FormatInt(revision, 10) {
			continue
		}
		for _, owner := range rs.OwnerReferences {
			if owner.UID == d.UID && owner.Kind == "Deployment" && owner.Controller != nil && *owner.Controller {
				if found != nil {
					return nil, inv.ErrConflict
				}
				found = rs
			}
		}
	}
	if found == nil {
		return nil, inv.ErrNotFound
	}
	if found.UID == "" || found.ResourceVersion == "" {
		return nil, inv.ErrConflict
	}
	return found, nil
}

func (s *server) prepareAction(ctx context.Context, cs kubernetes.Interface, d *appsv1.Deployment, a inv.Action, r *inv.Review) error {
	r.Action = a
	desired := d.DeepCopy()
	path := "/spec/template"
	switch a.Kind {
	case "scale":
		r.After = *a.Replicas
		desired.Spec.Replicas = a.Replicas
		path = "/spec/replicas"
	case "image":
		found := false
		imageIndex := -1
		for i := range desired.Spec.Template.Spec.Containers {
			if desired.Spec.Template.Spec.Containers[i].Name == a.Container {
				desired.Spec.Template.Spec.Containers[i].Image = a.Image
				found = true
				imageIndex = i
			}
		}
		if !found {
			return inv.ErrNotFound
		}
		path = fmt.Sprintf("/spec/template/spec/containers/%d/image", imageIndex)
	case "restart":
		if desired.Spec.Template.Annotations == nil {
			desired.Spec.Template.Annotations = map[string]string{}
		}
		r.RestartAt = time.Now().UTC().Format(time.RFC3339Nano)
		desired.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = r.RestartAt
		path = "/spec/template/metadata/annotations"
	case "rollback":
		rs, err := ownedRevision(ctx, cs, d, a.Revision)
		if err != nil {
			return err
		}
		r.RollbackUID = string(rs.UID)
		r.RollbackVersion = rs.ResourceVersion
		desired.Spec.Template = *rs.Spec.Template.DeepCopy()
		delete(desired.Spec.Template.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
	default:
		return inv.ErrInvalid
	}
	var value any = desired.Spec.Template
	if a.Kind == "scale" {
		value = r.After
	} else {
		if a.Kind == "image" {
			value = a.Image
		}
		if a.Kind == "restart" {
			value = desired.Spec.Template.Annotations
		}
		r.TemplateDigest = digest(desired.Spec.Template)
		r.Warnings = append(r.Warnings, "Pod template changes replace pods through the Deployment rollout strategy.")
	}
	r.Patch, _ = json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": r.Target.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": r.ResourceVersion},
		{"op": "add", "path": path, "value": value},
	})
	r.MutationDigest = digest(r.Patch)
	if a.Kind == "scale" {
		return nil
	}
	r.Noop = digest(d.Spec.Template) == digest(desired.Spec.Template)
	visibleBefore, visibleAfter := maskedTemplates(d.Spec.Template, desired.Spec.Template)
	before, _ := yaml.Marshal(visibleBefore)
	after, _ := yaml.Marshal(visibleAfter)
	edits := myers.ComputeEdits(span.URIFromPath("live-template"), string(before), string(after))
	r.Diff = s.redactText(fmt.Sprint(gotextdiff.ToUnified("live-template", "reviewed-template", string(before), edits)))
	r.Diff += "\nOnly the reviewed template fields change. UID and resourceVersion are atomically tested. Sensitive values may be redacted.\n"
	if a.Kind == "rollback" {
		r.Diff += fmt.Sprintf("Pinned ReplicaSet UID %s, resourceVersion %s, revision %d\n", r.RollbackUID, r.RollbackVersion, a.Revision)
	}
	return nil
}

// Hide all literal environment values, including unlabeled credentials. Distinct
// markers retain the fact that a value changed without saving executable values.
func maskedTemplates(before, after corev1.PodTemplateSpec) (corev1.PodTemplateSpec, corev1.PodTemplateSpec) {
	b, a := before.DeepCopy(), after.DeepCopy()
	originals := map[string]string{}
	mask := func(containers []corev1.Container, prefix string, isAfter bool) {
		for i := range containers {
			for j := range containers[i].Env {
				e := &containers[i].Env[j]
				if e.Value == "" {
					continue
				}
				key := prefix + containers[i].Name + "/" + e.Name
				if isAfter {
					if originals[key] != e.Value {
						e.Value = "[REDACTED changed env value]"
					} else {
						e.Value = "[REDACTED env value]"
					}
				} else {
					originals[key] = e.Value
					e.Value = "[REDACTED env value]"
				}
			}
		}
	}
	mask(b.Spec.Containers, "regular/", false)
	mask(b.Spec.InitContainers, "init/", false)
	mask(a.Spec.Containers, "regular/", true)
	mask(a.Spec.InitContainers, "init/", true)
	return *b, *a
}
