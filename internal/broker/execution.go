package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yunus/safina/internal/broker/api"
	"github.com/yunus/safina/internal/broker/operator"
	inv "github.com/yunus/safina/internal/investigation"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (s *server) operator(w http.ResponseWriter, req *http.Request) {
	var r api.Request
	var result any
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, requestBytes))
	if err == nil {
		err = decode(body, &r)
	}
	if err == nil && (r.Version != api.Version || r.ID == "") {
		err = inv.ErrInvalid
	}
	if err == nil {
		result, err = s.dispatchOperator(req.Context(), r)
	}
	response := api.Response{Version: api.Version, ID: r.ID}
	if err == nil {
		response.Result, err = json.Marshal(result)
	}
	if err != nil {
		response.Error = publicError(err)
	}
	writeJSON(w, response)
}

func (s *server) dispatchOperator(ctx context.Context, r api.Request) (any, error) {
	if r.Method == "inbox" {
		var empty struct{}
		if err := decode(r.Params, &empty); err != nil {
			return nil, err
		}
		out := operator.Inbox{Investigations: s.store.Index()}
		out.Warnings, _ = s.health()
		for i := range out.Investigations {
			out.Investigations[i] = s.publicInvestigation(out.Investigations[i])
			current := &out.Investigations[i]
			for _, p := range current.Proposals {
				if p.State == "pending" {
					out.Pending++
				}
			}
			for j := range current.Executions {
				current.Executions[j].Claim = ""
			}
		}
		return out, nil
	}
	var p operator.Request
	if err := decode(r.Params, &p); err != nil {
		return nil, err
	}
	// Reject irrelevant fields too: these are typed operations, not a generic
	// envelope capable of smuggling approval into a preparation or revision.
	if r.Method != "approve" && (p.Review != "" || p.Digest != "" || len(p.Overrides) != 0) {
		return nil, inv.ErrInvalid
	}
	if r.Method != "revise" && (p.Replicas != nil || p.Action != nil) {
		return nil, inv.ErrInvalid
	}
	switch r.Method {
	case "prepare":
		return s.prepare(ctx, p)
	case "approve":
		return s.approve(ctx, p)
	case "reject":
		return struct{}{}, s.store.Reject(p.Investigation, p.Proposal, p.Revision, operatorIdentity())
	case "revise":
		if (p.Replicas == nil) == (p.Action == nil) {
			return nil, inv.ErrInvalid
		}
		current, err := s.store.Get(p.Investigation)
		if err != nil {
			return nil, err
		}
		old, err := proposal(current, p)
		if err != nil {
			return nil, err
		}
		if old.Alternative.Action.Kind == "restart" {
			return nil, inv.ErrInvalid
		}
		a := old.Alternative
		if p.Action != nil {
			a.Action = *p.Action
		} else {
			a.Action.Replicas = p.Replicas
		}
		if err = a.Action.Validate(); err != nil {
			return nil, err
		}
		return s.store.Revise(current.ID, old.ID, old.Revision, operatorIdentity(), a)
	default:
		return nil, inv.ErrInvalid
	}
}

func (s *server) approve(parent context.Context, p operator.Request) (inv.Execution, error) {
	// Serialize review consumption through final send; store selection also
	// atomically excludes sibling alternatives and duplicate callers.
	s.reviewMu.Lock()
	defer s.reviewMu.Unlock()
	r, ok := s.reviews[p.Review]
	if !ok || !validApproval(p, r) {
		return inv.Execution{}, inv.ErrConflict
	}
	current, err := s.store.Get(p.Investigation)
	if err != nil {
		return inv.Execution{}, err
	}
	// Lost-response retries can return the persisted result but never send again.
	for _, decision := range current.Decisions {
		if decision.Review == r.ID && decision.Digest == r.Digest {
			for _, op := range current.Executions {
				if op.ID == decision.Operation {
					op.Claim = ""
					return op, nil
				}
			}
		}
	}
	if _, err = proposal(current, p); err != nil {
		return inv.Execution{}, err
	}
	// Explicit approval survives a closing UI. This bounded request finishes
	// before HTTP shutdown releases the journal lock.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 8*time.Second)
	defer cancel()
	cs, err := s.clusterClient(current)
	if err != nil {
		return inv.Execution{}, err
	}
	d, err := cs.AppsV1().Deployments(r.Target.Namespace).Get(ctx, r.Target.Name, metav1.GetOptions{})
	if err != nil {
		return inv.Execution{}, err
	}
	if string(d.UID) != r.Target.UID || d.ResourceVersion != r.ResourceVersion {
		return inv.Execution{}, inv.ErrConflict
	}
	_, _, signature := controllerWarnings(ctx, cs, d)
	if signature != r.ControllerSignature {
		return inv.Execution{}, inv.ErrConflict
	}
	if r.Action.Kind == "rollback" {
		rs, revisionErr := ownedRevision(ctx, cs, d, r.Action.Revision)
		if revisionErr != nil || string(rs.UID) != r.RollbackUID || rs.ResourceVersion != r.RollbackVersion {
			return inv.Execution{}, inv.ErrConflict
		}
	}
	// Both the journal and logbook must be writable BEFORE the request. A failed
	// post-intent delivery retires this approval; it never leaves a reusable send.
	if err = s.store.Deliver(s.history.AppendDurable); err != nil {
		return inv.Execution{}, inv.ErrRecording
	}
	op, fresh, err := s.store.SelectReviewed(r, operatorIdentity(), p.Overrides)
	if err != nil {
		return inv.Execution{}, err
	}
	if !fresh {
		op.Claim = ""
		return op, nil
	}
	if err = s.store.Deliver(s.history.AppendDurable); err != nil {
		op.State = "failed"
		op.Detail = "No request sent: approval logbook delivery failed; new review required."
		_ = s.store.RecordOutcome(current.ID, op)
		return inv.Execution{}, inv.ErrRecording
	}
	patch := r.Patch

	// Explicitly disable even Retry-After retries. Every approval grants one
	// HTTP request, including when the server returns an ambiguous 503.
	accepted := &appsv1.Deployment{}
	sendErr := cs.AppsV1().RESTClient().Patch(types.JSONPatchType).Namespace(r.Target.Namespace).Resource("deployments").Name(r.Target.Name).Param("fieldManager", "safina-broker").Body(patch).MaxRetries(0).Do(ctx).Into(accepted)
	switch {
	case sendErr != nil:
		op.State = "unknown"
		op.Detail = "Request outcome uncertain; only read-only reconciliation will follow. No automatic replay."
		if apierrors.IsConflict(sendErr) || apierrors.IsInvalid(sendErr) || apierrors.IsForbidden(sendErr) || apierrors.IsUnauthorized(sendErr) || apierrors.IsNotFound(sendErr) || apierrors.IsBadRequest(sendErr) {
			op.State = "failed"
			op.Detail = "API rejected the reviewed write; refresh and review again."
		}
	case string(accepted.UID) != r.Target.UID || accepted.ResourceVersion == "" || accepted.Generation <= r.Generation || accepted.Spec.Replicas == nil || *accepted.Spec.Replicas != r.After || (r.TemplateDigest != "" && digest(accepted.Spec.Template) != r.TemplateDigest):
		op.State = "unknown"
		op.Detail = "API response did not establish the reviewed change; read-only reconciliation required."
	default:
		op.State = "accepted"
		op.UID = string(accepted.UID)
		op.ResourceVersion = accepted.ResourceVersion
		op.Generation = accepted.Generation
		op.Detail = "API accepted the reviewed change; awaiting controller observation. Application recovery is unverified."
	}
	if err = s.store.RecordOutcome(current.ID, op); err != nil {
		return inv.Execution{}, err
	}
	s.deliver()
	op.Claim = ""
	return op, nil
}

// Reconcile performs reads only, including on startup after an interrupted send.
// Accepted responses have durable UID/generation provenance. An ambiguous send
// never becomes completed merely because replicas happen to match.
func (s *server) monitor(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) reconcile(parent context.Context) {
	for _, current := range s.store.Active() {
		for _, op := range current.Executions {
			if parent.Err() != nil {
				return
			}
			if op.State != "accepted" && op.State != "unknown" {
				continue
			}
			var review *inv.Review
			for _, d := range current.Decisions {
				if d.ID == op.Decision {
					review = d.Prepared
				}
			}
			if review == nil {
				continue
			}
			s.reconcileOne(parent, current, op, *review)
		}
	}
}

func (s *server) reconcileOne(parent context.Context, current inv.Investigation, op inv.Execution, r inv.Review) {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	cs, err := s.clusterClient(current)
	if err != nil {
		op.Detail = "Read-only monitoring unavailable: cluster configuration or identity could not be validated. No replay; refresh configuration to resume observations."
		_ = s.store.RecordOutcome(current.ID, op)
		return
	}
	d, err := cs.AppsV1().Deployments(r.Target.Namespace).Get(ctx, r.Target.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		op.Detail = "Read-only monitoring unavailable: API read failed. Outcome not verified; monitoring will retry reads only."
		_ = s.store.RecordOutcome(current.ID, op)
		return
	}
	switch {
	case apierrors.IsNotFound(err) || string(d.UID) != r.Target.UID:
		op.State = "superseded"
		op.Detail = "Target removed or replaced; no write will be replayed."
	case op.State == "unknown":
		replicas := int32(1)
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}
		// Stable detail avoids journal growth on every periodic read.
		op.Detail = fmt.Sprintf("Read-only reconciliation: desired replicas currently %d (requested %d). Causal outcome remains unknown; no replay. Application recovery unverified.", replicas, r.After)
		if r.TemplateDigest != "" {
			op.Detail += fmt.Sprintf(" Template matches reviewed digest: %t.", digest(d.Spec.Template) == r.TemplateDigest)
		}
	case d.Generation != op.Generation:
		op.State = "superseded"
		op.Detail = "A later Deployment generation superseded this operation."
	case d.Status.ObservedGeneration >= op.Generation:
		if d.Status.Replicas == r.After && d.Status.UpdatedReplicas == r.After && d.Status.AvailableReplicas == r.After {
			op.State = "completed"
			op.Detail = "Controller observed the accepted generation and all desired replicas are updated and available. Application recovery unverified."
		}
	}
	if op.State == "accepted" {
		op.Detail = "Awaiting controller observation and desired updated/available replicas. Application recovery unverified."
	}
	if d != nil && (op.State == "completed" || op.State == "accepted") && d.Status.ObservedGeneration >= op.Generation {
		for _, condition := range d.Status.Conditions {
			if condition.Reason == "ProgressDeadlineExceeded" && condition.Type == "Progressing" {
				op.State = "failed"
				op.Detail = "Controller reports ProgressDeadlineExceeded; application recovery unverified."
			}
		}
	}
	if err = s.store.RecordOutcome(current.ID, op); err != nil && !errors.Is(err, inv.ErrConflict) {
		s.mu.Lock()
		s.deliveryError = "Outcome recording unavailable; no write will be replayed"
		s.mu.Unlock()
	}
}
