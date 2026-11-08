package broker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/broker/api"

	"github.com/YunusAndreasson/safina/internal/broker/operator"
	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
	"github.com/YunusAndreasson/safina/internal/investigation/store"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type scaleCluster struct {
	mu             sync.Mutex
	deployment     appsv1.Deployment
	hpas           autoscalingv2.HorizontalPodAutoscalerList
	hpaDenied      bool
	replicaSets    []appsv1.ReplicaSet
	readDenied     bool
	logText        string
	logInterrupted bool
	race           bool
	response       string
	writes         int
	patches        []map[string]any
}

func scaleFixture(t *testing.T) (*server, *scaleCluster, []operator.Request) {
	t.Helper()
	replicas := int32(2)
	cluster := &scaleCluster{deployment: appsv1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "original", ResourceVersion: "10", Generation: 4}, Spec: appsv1.DeploymentSpec{Replicas: &replicas}}}
	httpServer := httptest.NewServer(http.HandlerFunc(cluster.serve))
	t.Cleanup(httpServer.Close)
	root := t.TempDir()
	o := Options{StateDir: filepath.Join(root, "state"), Kubeconfig: filepath.Join(root, "config"), History: history.Options{Directory: filepath.Join(root, "history")}}
	require.NoError(t, os.WriteFile(o.Kubeconfig, []byte(fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: cluster\n  cluster:\n    server: %s\ncontexts:\n- name: explicit\n  context:\n    cluster: cluster\n    user: unused\n", httpServer.URL)), 0o600))
	st, err := store.Open(o.StateDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	journal, err := history.Open(o.History)
	require.NoError(t, err)
	t.Cleanup(func() { _ = journal.Close() })
	s := &server{opts: o, store: st, history: journal}
	current, err := st.Start(inv.Investigation{Session: "agent", Cluster: clusterFingerprint(httpServer.URL, nil), Context: "explicit", Summary: "test"}, "start")
	require.NoError(t, err)
	desired := int32(3)
	a := inv.Alternative{Target: inv.Target{Cluster: current.Cluster, APIVersion: "apps/v1", Kind: "Deployment", Namespace: "shop", Name: "api", UID: "original"}, Observed: inv.Observed{ResourceVersion: "10", Generation: 4, Time: time.Now()}, Action: inv.Action{Kind: "scale", Replicas: &desired}}
	_, err = st.Submit(current.ID, "agent", "group", []inv.Alternative{a, a})
	require.NoError(t, err)
	current, err = st.Get(current.ID)
	require.NoError(t, err)
	requests := make([]operator.Request, 0, len(current.Proposals))
	for _, p := range current.Proposals {
		requests = append(requests, operator.Request{Investigation: current.ID, Proposal: p.ID, Revision: p.Revision})
	}
	return s, cluster, requests
}

func (c *scaleCluster) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/apis/autoscaling/v2/namespaces/shop/horizontalpodautoscalers" {
		if c.hpaDenied {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		c.hpas.TypeMeta = metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscalerList"}
		writeJSON(w, c.hpas)
		return
	}
	if c.readDenied && r.Method == http.MethodGet {
		w.WriteHeader(403)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/replicasets") {
		writeJSON(w, appsv1.ReplicaSetList{TypeMeta: metav1.TypeMeta{Kind: "ReplicaSetList", APIVersion: "apps/v1"}, Items: c.replicaSets})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/pods") {
		writeJSON(w, corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/events") {
		writeJSON(w, corev1.EventList{TypeMeta: metav1.TypeMeta{Kind: "EventList", APIVersion: "v1"}})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/log") {
		if c.logInterrupted {
			w.Header().Set("Content-Length", strconv.Itoa(len(c.logText)+100))
		}
		_, _ = w.Write([]byte(c.logText))
		return
	}
	if r.URL.Path != "/apis/apps/v1/namespaces/shop/deployments/api" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPatch {
		c.writes++
		if c.response == "retry" {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&c.patches); err != nil {
			w.WriteHeader(400)
			return
		}
		if c.race {
			c.deployment.ResourceVersion = "raced"
		}
		if len(c.patches) != 3 || c.patches[0]["value"] != string(c.deployment.UID) || c.patches[1]["value"] != c.deployment.ResourceVersion {
			w.WriteHeader(http.StatusConflict)
			return
		}
		value, err := json.Marshal(c.patches[2]["value"])
		if err != nil {
			http.Error(w, "invalid patch value", http.StatusBadRequest)
			return
		}
		switch c.patches[2]["path"] {
		case "/spec/replicas":
			_ = json.Unmarshal(value, &c.deployment.Spec.Replicas)
		case "/spec/template/spec/containers/0/image":
			_ = json.Unmarshal(value, &c.deployment.Spec.Template.Spec.Containers[0].Image)
		case "/spec/template/metadata/annotations":
			_ = json.Unmarshal(value, &c.deployment.Spec.Template.Annotations)
		case "/spec/template":
			c.deployment.Spec.Template = corev1.PodTemplateSpec{}
			_ = json.Unmarshal(value, &c.deployment.Spec.Template)
		default:
			w.WriteHeader(400)
			return
		}
		c.deployment.Generation++
		c.deployment.ResourceVersion = strconv.FormatInt(c.deployment.Generation+6, 10)
		if c.response == "lost" {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
	}
	writeJSON(w, c.deployment)
}

func approval(r inv.Review) operator.Request {
	return operator.Request{Investigation: r.Investigation, Proposal: r.Proposal, Revision: r.Revision, Review: r.ID, Digest: r.Digest, Overrides: r.RequiredOverrides}
}

func TestReviewedScaleSingleClaimAndControllerCompletion(t *testing.T) {
	s, c, requests := scaleFixture(t)
	first, err := s.prepare(t.Context(), requests[0])
	require.NoError(t, err)
	second, err := s.prepare(t.Context(), requests[1])
	require.NoError(t, err)
	require.Equal(t, int32(2), first.Before)
	require.Equal(t, int32(3), first.After)
	var wg sync.WaitGroup
	for _, r := range []inv.Review{first, second, first, second} {
		wg.Go(func() { _, _ = s.approve(t.Context(), approval(r)) })
	}
	wg.Wait()
	c.mu.Lock()
	require.Equal(t, 1, c.writes)
	require.Equal(t, []map[string]any{{"op": "test", "path": "/metadata/uid", "value": "original"}, {"op": "test", "path": "/metadata/resourceVersion", "value": "10"}, {"op": "add", "path": "/spec/replicas", "value": float64(3)}}, c.patches)
	c.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 5, Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3}
	c.mu.Unlock()
	s.reconcile(t.Context())
	current, err := s.store.Get(first.Investigation)
	require.NoError(t, err)
	require.Len(t, current.Decisions, 1)
	require.Len(t, current.Executions, 1)
	require.Equal(t, "completed", current.Executions[0].State)
	require.NotNil(t, current.Decisions[0].Prepared)
	s.deliver()
	records, err := history.Query(s.history.Directory(), history.Filter{Cluster: current.Cluster})
	require.NoError(t, err)
	found := false
	for _, r := range records.Records {
		if r.Outcome == "completed" {
			found = true
			require.Equal(t, "api", r.Target.Name)
			require.Equal(t, current.Decisions[0].ID, r.Decision)
			require.Equal(t, "3", r.Parameters["replicas.after"])
		}
	}
	require.True(t, found)
}

func TestScaleRejectsStaleIdentityVersionAndControllerReview(t *testing.T) {
	for _, change := range []string{"uid", "version", "hpa", "race", "noop", "digest", "overrides", "expired"} {
		t.Run(change, func(t *testing.T) {
			s, c, requests := scaleFixture(t)
			if change == "noop" {
				c.deployment.Spec.Replicas = new(int32(3))
			}
			r, err := s.prepare(t.Context(), requests[0])
			require.NoError(t, err)
			p := approval(r)
			switch change {
			case "uid":
				c.deployment.UID = "replacement"
			case "version":
				c.deployment.ResourceVersion = "12"
			case "hpa":
				c.hpaDenied = true
			case "race":
				c.race = true
			case "digest":
				p.Digest = "forged"
			case "overrides":
				p.Overrides = []string{"not-reviewed"}
			case "expired":
				r.Time = time.Now().Add(-time.Hour)
				s.reviews[r.ID] = r
			}
			op, err := s.approve(t.Context(), p)
			if change == "race" {
				require.NoError(t, err)
				require.Equal(t, "failed", op.State)
				require.Equal(t, 1, c.writes)
				require.Equal(t, int32(2), *c.deployment.Spec.Replicas)
			} else {
				require.Error(t, err)
				require.Zero(t, c.writes)
			}
		})
	}
}

func TestScaleOverridesAreExplicitAndJournaled(t *testing.T) {
	s, c, requests := scaleFixture(t)
	c.deployment.Labels = map[string]string{"argocd.argoproj.io/instance": "app"}
	c.hpas.Items = []autoscalingv2.HorizontalPodAutoscaler{{ObjectMeta: metav1.ObjectMeta{Name: "auto", UID: "hpa"}, Spec: autoscalingv2.HorizontalPodAutoscalerSpec{ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}}}}
	r, err := s.prepare(t.Context(), requests[0])
	require.NoError(t, err)
	require.Equal(t, []string{"gitops", "hpa"}, r.RequiredOverrides)
	p := approval(r)
	p.Overrides = nil
	_, err = s.approve(t.Context(), p)
	require.ErrorIs(t, err, inv.ErrConflict)
	require.Zero(t, c.writes)
	op, err := s.approve(t.Context(), approval(r))
	require.NoError(t, err)
	require.Equal(t, "accepted", op.State)
	current, err := s.store.Get(r.Investigation)
	require.NoError(t, err)
	require.Equal(t, r.RequiredOverrides, current.Decisions[0].Overrides)
}

func TestScaleRecordingFailureNeverSends(t *testing.T) {
	for _, which := range []string{"history", "journal"} {
		t.Run(which, func(t *testing.T) {
			s, c, requests := scaleFixture(t)
			r, err := s.prepare(t.Context(), requests[0])
			require.NoError(t, err)
			if which == "history" {
				require.NoError(t, s.history.Close())
			} else {
				require.NoError(t, s.store.Close())
			}
			_, err = s.approve(t.Context(), approval(r))
			require.Error(t, err)
			require.Zero(t, c.writes)
		})
	}
}

func TestScaleAmbiguityAndRestartNeverReplay(t *testing.T) {
	for _, response := range []string{"lost", "retry", "accepted"} {
		t.Run(response, func(t *testing.T) {
			s, c, requests := scaleFixture(t)
			c.response = response
			r, err := s.prepare(t.Context(), requests[0])
			require.NoError(t, err)
			op, err := s.approve(t.Context(), approval(r))
			require.NoError(t, err)
			if response == "accepted" {
				require.Equal(t, "accepted", op.State)
			} else {
				require.Equal(t, "unknown", op.State)
			}
			require.NoError(t, s.store.Close())
			s.store, err = store.Open(s.opts.StateDir)
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.store.Close() })
			c.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 5, Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3}
			s.reconcile(t.Context())
			s.reconcile(t.Context())
			current, err := s.store.Get(r.Investigation)
			require.NoError(t, err)
			if response == "accepted" {
				require.Equal(t, "completed", current.Executions[0].State)
			} else {
				require.Equal(t, "unknown", current.Executions[0].State)
				require.Contains(t, current.Executions[0].Detail, "Causal outcome remains unknown")
			}
			require.Equal(t, 1, c.writes)
		})
	}
}

func TestScaleIntentRecoveryIsReadOnly(t *testing.T) {
	s, c, requests := scaleFixture(t)
	r, err := s.prepare(t.Context(), requests[0])
	require.NoError(t, err)
	_, fresh, err := s.store.SelectReviewed(r, operatorIdentity(), nil)
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, s.store.Close())
	s.store, err = store.Open(s.opts.StateDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.store.Close() })
	s.reconcile(t.Context())
	current, err := s.store.Get(r.Investigation)
	require.NoError(t, err)
	require.Equal(t, "unknown", current.Executions[0].State)
	require.Zero(t, c.writes)
}

func TestOperatorRevisionAndRejectionInvalidateReviews(t *testing.T) {
	s, c, requests := scaleFixture(t)
	r, err := s.prepare(t.Context(), requests[0])
	require.NoError(t, err)
	p := requests[0]
	p.Replicas = new(int32(0))
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	result, err := s.dispatchOperator(t.Context(), api.Request{Method: "revise", Params: raw})
	require.NoError(t, err)
	revised, ok := result.(inv.Proposal)
	require.True(t, ok)
	require.Equal(t, 2, revised.Revision)
	_, err = s.approve(t.Context(), approval(r))
	require.ErrorIs(t, err, inv.ErrConflict)
	p.Revision = revised.Revision
	p.Replicas = nil
	r, err = s.prepare(t.Context(), p)
	require.NoError(t, err)
	require.Zero(t, r.After)
	require.Contains(t, strings.Join(r.Warnings, " "), "Scale to zero")
	raw, err = json.Marshal(p)
	require.NoError(t, err)
	_, err = s.dispatchOperator(t.Context(), api.Request{Method: "reject", Params: raw})
	require.NoError(t, err)
	_, err = s.approve(t.Context(), approval(r))
	require.ErrorIs(t, err, inv.ErrConflict)
	require.Zero(t, c.writes)
}

func TestChangedKubeconfigCannotRetargetApproval(t *testing.T) {
	s, c, requests := scaleFixture(t)
	r, err := s.prepare(t.Context(), requests[0])
	require.NoError(t, err)
	raw, err := os.ReadFile(s.opts.Kubeconfig)
	require.NoError(t, err)
	changed := strings.Replace(string(raw), "server: http://", "server: https://", 1)
	// #nosec G703 -- Path is created by this test fixture under t.TempDir, never supplied by an API caller.
	require.NoError(t, os.WriteFile(s.opts.Kubeconfig, []byte(changed), 0o600))
	_, err = s.approve(t.Context(), approval(r))
	require.ErrorIs(t, err, inv.ErrConflict)
	require.Zero(t, c.writes)
}

func TestAcceptedReadinessFailureAndSupersession(t *testing.T) {
	for _, change := range []string{"generation", "uid", "deadline", "not-observed"} {
		t.Run(change, func(t *testing.T) {
			s, c, requests := scaleFixture(t)
			r, err := s.prepare(t.Context(), requests[0])
			require.NoError(t, err)
			_, err = s.approve(t.Context(), approval(r))
			require.NoError(t, err)
			c.deployment.Status = appsv1.DeploymentStatus{ObservedGeneration: 5, Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3}
			expected := "superseded"
			switch change {
			case "generation":
				c.deployment.Generation++
			case "uid":
				c.deployment.UID = "new"
			case "deadline":
				c.deployment.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentProgressing, Reason: "ProgressDeadlineExceeded"}}
				expected = "failed"
			case "not-observed":
				c.deployment.Status.ObservedGeneration = 4
				expected = "accepted"
			}
			s.reconcile(t.Context())
			current, err := s.store.Get(r.Investigation)
			require.NoError(t, err)
			require.Equal(t, expected, current.Executions[0].State)
			require.Equal(t, 1, c.writes)
		})
	}
}
