package broker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/broker/operator"
	"github.com/yunus/safina/internal/history"
	inv "github.com/yunus/safina/internal/investigation"
	"github.com/yunus/safina/internal/investigation/store"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAllTemplateActionsPreserveReviewedChange(t *testing.T) {
	for _, kind := range []string{"image", "restart", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			s, c, requests := scaleFixture(t)
			c.deployment.Spec.Template = corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}, Annotations: map[string]string{"keep": "yes"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "old", Env: []corev1.EnvVar{{Name: "PASSWORD", Value: "password=supersecret"}}}, {Name: "sidecar", Image: "side:v1"}}}}
			rs := appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-old", UID: "owned-rs", ResourceVersion: "rs10", Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"}, OwnerReferences: []metav1.OwnerReference{{UID: "original", Kind: "Deployment", Controller: new(true)}}}, Spec: appsv1.ReplicaSetSpec{Template: *c.deployment.Spec.Template.DeepCopy()}}
			rs.Spec.Template.Spec.Containers[0].Image = "pinned:v1"
			rs.Spec.Template.Labels[appsv1.DefaultDeploymentUniqueLabelKey] = "hash"
			var patches []map[string]any
			writes := 0
			var mu sync.Mutex
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "horizontalpodautoscalers") {
					_ = json.NewEncoder(w).Encode(autoscalingv2.HorizontalPodAutoscalerList{TypeMeta: metav1.TypeMeta{Kind: "HorizontalPodAutoscalerList", APIVersion: "autoscaling/v2"}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "replicasets") {
					_ = json.NewEncoder(w).Encode(appsv1.ReplicaSetList{TypeMeta: metav1.TypeMeta{Kind: "ReplicaSetList", APIVersion: "apps/v1"}, Items: []appsv1.ReplicaSet{rs}})
					return
				}
				if r.Method == http.MethodPatch {
					writes++
					if err := json.NewDecoder(r.Body).Decode(&patches); err != nil {
						w.WriteHeader(400)
						return
					}
					if patches[0]["value"] != "original" || patches[1]["value"] != c.deployment.ResourceVersion {
						w.WriteHeader(409)
						return
					}
					value, _ := json.Marshal(patches[2]["value"])
					switch kind {
					case "image":
						_ = json.Unmarshal(value, &c.deployment.Spec.Template.Spec.Containers[0].Image)
					case "restart":
						_ = json.Unmarshal(value, &c.deployment.Spec.Template.Annotations)
					case "rollback":
						_ = json.Unmarshal(value, &c.deployment.Spec.Template)
					}
					c.deployment.Generation++
					c.deployment.ResourceVersion = "11"
				}
				_ = json.NewEncoder(w).Encode(c.deployment)
			}))
			defer endpoint.Close()
			// Replace the fixture's config and start a new investigation pinned to it.
			configText := "apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: " + endpoint.URL + "\ncontexts:\n- name: explicit\n  context:\n    cluster: cluster\n"
			require.NoError(t, os.WriteFile(s.opts.Kubeconfig, []byte(configText), 0o600))
			current, err := s.store.Start(inv.Investigation{Session: "template", Cluster: clusterFingerprint(endpoint.URL, nil), Context: "explicit"}, "start")
			require.NoError(t, err)
			old, err := s.store.Get(requests[0].Investigation)
			require.NoError(t, err)
			a := old.Proposals[0].Alternative
			a.Target.Cluster = current.Cluster
			a.Action = inv.Action{Kind: kind}
			if kind == "image" {
				a.Action.Container = "api"
				a.Action.Image = "new:v2"
			}
			if kind == "rollback" {
				a.Action.Revision = 2
			}
			_, err = s.store.Submit(current.ID, "template", "submit", []inv.Alternative{a})
			require.NoError(t, err)
			current, err = s.store.Get(current.ID)
			require.NoError(t, err)
			p := operator.Request{Investigation: current.ID, Proposal: current.Proposals[0].ID, Revision: 1}
			r, err := s.prepare(t.Context(), p)
			require.NoError(t, err)
			require.NotEmpty(t, r.Patch)
			require.NotContains(t, r.Diff, "supersecret")
			if kind == "rollback" {
				original := rs.ResourceVersion
				rs.ResourceVersion = "changed"
				_, err = s.approve(t.Context(), approval(r))
				require.ErrorIs(t, err, inv.ErrConflict)
				require.Zero(t, writes)
				rs.ResourceVersion = original
			}
			op, err := s.approve(t.Context(), approval(r))
			require.NoError(t, err)
			require.Equal(t, "accepted", op.State)
			require.Equal(t, 1, writes)
			require.Equal(t, "side:v1", c.deployment.Spec.Template.Spec.Containers[1].Image)
			require.Equal(t, "yes", c.deployment.Spec.Template.Annotations["keep"])
			require.Equal(t, "password=supersecret", c.deployment.Spec.Template.Spec.Containers[0].Env[0].Value)
			if kind == "restart" {
				require.Equal(t, r.RestartAt, c.deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])
			}
			if kind == "rollback" {
				require.Equal(t, "pinned:v1", c.deployment.Spec.Template.Spec.Containers[0].Image)
				require.NotContains(t, c.deployment.Spec.Template.Labels, appsv1.DefaultDeploymentUniqueLabelKey)
			}
			c.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 5, Replicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2}
			s.reconcile(t.Context())
			current, err = s.store.Get(current.ID)
			require.NoError(t, err)
			require.Equal(t, "completed", current.Executions[0].State)
			saved, err := os.ReadFile(filepath.Join(s.opts.StateDir, "journal.jsonl"))
			require.NoError(t, err)
			require.NotContains(t, string(saved), "supersecret")
			require.NoError(t, s.store.Close())
			s.store, err = store.Open(s.opts.StateDir)
			require.NoError(t, err)
			defer func() { _ = s.store.Close() }()
			current, err = s.store.Get(current.ID)
			require.NoError(t, err)
			require.Empty(t, current.Decisions[0].Prepared.Patch)
			s.deliver()
			results, err := history.Query(s.history.Directory(), history.Filter{Cluster: current.Cluster, Since: time.Now().Add(-time.Hour)})
			require.NoError(t, err)
			require.NotEmpty(t, results.Records)
		})
	}
}

func TestTemplateDiffExcludesUnlabeledEnvironmentValues(t *testing.T) {
	before := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Env: []corev1.EnvVar{{Name: "ORDINARY_NAME", Value: "unlabeled-credential-one"}}}}}}
	after := *before.DeepCopy()
	after.Spec.Containers[0].Env[0].Value = "unlabeled-credential-two"
	b, a := maskedTemplates(before, after)
	bytes, err := json.Marshal([]corev1.PodTemplateSpec{b, a})
	require.NoError(t, err)
	require.NotContains(t, string(bytes), "unlabeled-credential")
	require.Contains(t, string(bytes), "changed env value")
	require.Equal(t, "unlabeled-credential-one", before.Spec.Containers[0].Env[0].Value)
	require.Equal(t, "unlabeled-credential-two", after.Spec.Containers[0].Env[0].Value)
}
