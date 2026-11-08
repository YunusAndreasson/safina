package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/yunus/safina/internal/broker/operator"
	"github.com/yunus/safina/internal/config"
	inv "github.com/yunus/safina/internal/investigation"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func (s *server) clusterClient(current inv.Investigation) (kubernetes.Interface, error) {
	// Resolve identity and REST config from the same snapshot: a kubeconfig change
	// between approval and execution must never retarget a prepared mutation.
	raw, err := config.LoadKubeConfig(s.opts.Kubeconfig)
	if err != nil {
		return nil, err
	}
	selected, ok := raw.Contexts[current.Context]
	if !ok || selected == nil {
		return nil, inv.ErrConflict
	}
	rc, err := clientcmd.NewNonInteractiveClientConfig(*raw, current.Context, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	ca := rc.CAData
	if len(ca) == 0 && rc.CAFile != "" {
		ca, err = os.ReadFile(rc.CAFile)
		if err != nil {
			return nil, err
		}
		rc.CAData = ca
		rc.CAFile = ""
	}
	if clusterFingerprint(rc.Host, ca) != current.Cluster {
		return nil, inv.ErrConflict
	}
	rc.Timeout = 6 * time.Second
	client, err := rest.HTTPClientFor(rc)
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return kubernetes.NewForConfigAndClient(rc, client)
}

func proposal(current inv.Investigation, p operator.Request) (inv.Proposal, error) {
	for _, item := range current.Proposals {
		if item.ID == p.Proposal && item.Revision == p.Revision {
			if item.State != "pending" {
				return item, inv.ErrConflict
			}
			return item, nil
		}
	}
	return inv.Proposal{}, inv.ErrNotFound
}

func digest(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func controllerWarnings(ctx context.Context, cs kubernetes.Interface, d *appsv1.Deployment) ([]string, []string, string, error) {
	warnings := []string{"Controller completion does not establish application recovery. Undetected external controllers may revert this change."}
	var required, identities []string
	hpas, err := cs.AutoscalingV2().HorizontalPodAutoscalers(d.Namespace).List(ctx, metav1.ListOptions{Limit: 100})
	if err != nil {
		warnings = append(warnings, "HPA discovery unavailable; controller coverage is incomplete.")
		required = append(required, "hpa-coverage")
	} else {
		if hpas.Continue != "" {
			warnings = append(warnings, "HPA discovery truncated at 100; controller coverage is incomplete.")
			required = append(required, "hpa-coverage")
		}
		for _, h := range hpas.Items {
			if (h.Spec.ScaleTargetRef.APIVersion == "apps/v1" || h.Spec.ScaleTargetRef.APIVersion == "") && h.Spec.ScaleTargetRef.Kind == "Deployment" && h.Spec.ScaleTargetRef.Name == d.Name {
				warnings = append(warnings, "HPA manages replicas and may change the desired count: "+h.Name)
				signature, digestErr := digest(h.Spec)
				if digestErr != nil {
					return nil, nil, "", digestErr
				}
				identities = append(identities, "hpa:"+string(h.UID)+":"+signature)
				if !contains(required, "hpa") {
					required = append(required, "hpa")
				}
			}
		}
	}
	managed := false
	for key, value := range d.Labels {
		if gitops(key + " " + value) {
			managed = true
		}
	}
	for key := range d.Annotations {
		if gitops(key) {
			managed = true
		}
	}
	for _, manager := range d.ManagedFields {
		if gitops(manager.Manager) {
			managed = true
		}
	}
	if managed {
		warnings = append(warnings, "GitOps management detected; reconciliation may reverse this scale.")
		required = append(required, "gitops")
	}
	sort.Strings(warnings)
	sort.Strings(required)
	sort.Strings(identities)
	signature, err := digest(struct{ Required, Identities []string }{required, identities})
	return warnings, required, signature, err
}

func gitops(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "argocd") || strings.Contains(s, "fluxcd") || strings.Contains(s, "kustomize-controller") || strings.Contains(s, "helm-controller")
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func (s *server) prepare(ctx context.Context, p operator.Request) (inv.Review, error) {
	current, err := s.store.Get(p.Investigation)
	if err != nil {
		return inv.Review{}, err
	}
	item, err := proposal(current, p)
	if err != nil {
		return inv.Review{}, err
	}
	a := item.Alternative
	if err = a.Action.Validate(); err != nil {
		return inv.Review{}, err
	}
	if s.redactText(a.Action.Image) != a.Action.Image || s.redactText(a.Action.Container) != a.Action.Container {
		return inv.Review{}, inv.ErrInvalid
	}
	cs, err := s.clusterClient(current)
	if err != nil {
		return inv.Review{}, err
	}
	d, err := cs.AppsV1().Deployments(a.Target.Namespace).Get(ctx, a.Target.Name, metav1.GetOptions{})
	if err != nil {
		return inv.Review{}, err
	}
	if d.UID != a.Target.UID || d.ResourceVersion == "" {
		return inv.Review{}, inv.ErrConflict
	}
	before := int32(1)
	if d.Spec.Replicas != nil {
		before = *d.Spec.Replicas
	}
	r := inv.Review{ID: inv.ID(), Investigation: current.ID, Proposal: item.ID, Revision: item.Revision, Target: a.Target, ResourceVersion: d.ResourceVersion, Generation: d.Generation, Before: before, After: before, Time: time.Now().UTC()}
	r.Warnings, r.RequiredOverrides, r.ControllerSignature, err = controllerWarnings(ctx, cs, d)
	if err != nil {
		return inv.Review{}, err
	}
	r.Drift = a.Observed.ResourceVersion != d.ResourceVersion
	if err = s.prepareAction(ctx, cs, d, a.Action, &r); err != nil {
		return inv.Review{}, err
	}
	if a.Action.Kind == "scale" {
		r.Noop = r.Before == r.After
	}
	if a.Action.Kind == "scale" && r.After == 0 {
		r.Warnings = append(r.Warnings, "Scale to zero: all Deployment replicas will stop.")
	}
	if r.Drift {
		r.Warnings = append(r.Warnings, "State changed since the proposal observation. This review uses the current resource version.")
	}
	if r.Noop {
		r.Warnings = append(r.Warnings, "No change: requested fields already match. Approval is disabled.")
	}
	if a.Action.Kind == "scale" {
		r.Diff = fmt.Sprintf("--- live Deployment.spec\n+++ reviewed Deployment.spec\n@@ replicas @@\n- replicas: %d\n+ replicas: %d\n\nOnly /spec/replicas changes. Preconditions: UID %s; resourceVersion %s.\n", r.Before, r.After, r.Target.UID, r.ResourceVersion)
	}
	r.History = s.reviewHistory(current, a.Target, a.Action.Kind)
	r.Digest, err = digest(r)
	if err != nil {
		return inv.Review{}, err
	}
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	if s.reviews == nil {
		s.reviews = map[string]inv.Review{}
	}
	for id, old := range s.reviews {
		if time.Since(old.Time) > 15*time.Minute || (old.Investigation == r.Investigation && old.Proposal == r.Proposal) {
			delete(s.reviews, id)
		}
	}
	if len(s.reviews) >= 1024 {
		return inv.Review{}, inv.ErrConflict
	}
	s.reviews[r.ID] = r
	return r, nil
}

func validApproval(p operator.Request, r inv.Review) bool {
	actual := append([]string(nil), p.Overrides...)
	sort.Strings(actual)
	return p.Investigation == r.Investigation && p.Proposal == r.Proposal && p.Revision == r.Revision && p.Digest == r.Digest && reflect.DeepEqual(actual, r.RequiredOverrides) && !r.Noop && time.Since(r.Time) <= 15*time.Minute
}

func operatorIdentity() string { return fmt.Sprintf("local-uid:%d", os.Getuid()) }
