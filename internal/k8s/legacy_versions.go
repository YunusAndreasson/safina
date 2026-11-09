package k8s

import (
	"context"
	"slices"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	autoscalingv1listers "k8s.io/client-go/listers/autoscaling/v1"
	autoscalingv2listers "k8s.io/client-go/listers/autoscaling/v2"
	batchv1listers "k8s.io/client-go/listers/batch/v1"
	batchv1beta1listers "k8s.io/client-go/listers/batch/v1beta1"
)

// Two kinds safina lists moved API versions within the clusters people still
// run: CronJobs reached batch/v1 in Kubernetes 1.21, and HPAs autoscaling/v2
// in 1.23. An older server only serves batch/v1beta1 and autoscaling/v1 (or
// v2beta2, which client-go no longer types). Safina reads those and converts
// them to the current types, so everything above this file sees one shape.

// servesResource reports whether the server serves resource in groupVersion.
// known is false when discovery couldn't tell (network, auth), so a caller
// keeps its default rather than guessing from an error.
func servesResource(d discovery.DiscoveryInterface, groupVersion, resource string) (serves, known bool) {
	list, err := d.ServerResourcesForGroupVersion(groupVersion)
	if apierrors.IsNotFound(err) {
		return false, true
	}
	if err != nil || list == nil {
		return false, false
	}
	return slices.ContainsFunc(list.APIResources, func(r metav1.APIResource) bool { return r.Name == resource }), true
}

// olderOnly reports a server that lacks resource in the current group version
// and serves it in the older one.
func olderOnly(d discovery.DiscoveryInterface, current, older, resource string) bool {
	if serves, known := servesResource(d, current, resource); serves || !known {
		return false
	}
	serves, known := servesResource(d, older, resource)
	return serves && known
}

func cronJobFromV1beta1(in *batchv1beta1.CronJob) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: in.ObjectMeta,
		Spec: batchv1.CronJobSpec{
			Schedule:                   in.Spec.Schedule,
			TimeZone:                   in.Spec.TimeZone,
			StartingDeadlineSeconds:    in.Spec.StartingDeadlineSeconds,
			ConcurrencyPolicy:          batchv1.ConcurrencyPolicy(in.Spec.ConcurrencyPolicy),
			Suspend:                    in.Spec.Suspend,
			JobTemplate:                batchv1.JobTemplateSpec{ObjectMeta: in.Spec.JobTemplate.ObjectMeta, Spec: in.Spec.JobTemplate.Spec},
			SuccessfulJobsHistoryLimit: in.Spec.SuccessfulJobsHistoryLimit,
			FailedJobsHistoryLimit:     in.Spec.FailedJobsHistoryLimit,
		},
		Status: batchv1.CronJobStatus{
			Active:             in.Status.Active,
			LastScheduleTime:   in.Status.LastScheduleTime,
			LastSuccessfulTime: in.Status.LastSuccessfulTime,
		},
	}
}

// hpaFromV1 carries what safina reads from an HPA (its target, bounds and
// current replicas); v1 has no metric specs to carry.
func hpaFromV1(in *autoscalingv1.HorizontalPodAutoscaler) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: in.ObjectMeta,
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind:       in.Spec.ScaleTargetRef.Kind,
				Name:       in.Spec.ScaleTargetRef.Name,
				APIVersion: in.Spec.ScaleTargetRef.APIVersion,
			},
			MinReplicas: in.Spec.MinReplicas,
			MaxReplicas: in.Spec.MaxReplicas,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			ObservedGeneration: in.Status.ObservedGeneration,
			LastScaleTime:      in.Status.LastScaleTime,
			CurrentReplicas:    in.Status.CurrentReplicas,
			DesiredReplicas:    in.Status.DesiredReplicas,
		},
	}
}

func convertAll[In, Out any](items []*In, err error, conv func(*In) *Out) ([]*Out, error) {
	if err != nil {
		return nil, err
	}
	out := make([]*Out, len(items))
	for i, it := range items {
		out[i] = conv(it)
	}
	return out, nil
}

// cronJobsFromV1beta1 serves a batch/v1beta1 CronJob cache through the
// batch/v1 lister interface. Every read returns fresh copies.
type cronJobsFromV1beta1 struct {
	l batchv1beta1listers.CronJobLister
}

func (a cronJobsFromV1beta1) List(sel labels.Selector) ([]*batchv1.CronJob, error) {
	items, err := a.l.List(sel)
	return convertAll(items, err, cronJobFromV1beta1)
}

func (a cronJobsFromV1beta1) CronJobs(ns string) batchv1listers.CronJobNamespaceLister {
	return cronJobsInNamespaceFromV1beta1{a.l.CronJobs(ns)}
}

type cronJobsInNamespaceFromV1beta1 struct {
	l batchv1beta1listers.CronJobNamespaceLister
}

func (a cronJobsInNamespaceFromV1beta1) List(sel labels.Selector) ([]*batchv1.CronJob, error) {
	items, err := a.l.List(sel)
	return convertAll(items, err, cronJobFromV1beta1)
}

func (a cronJobsInNamespaceFromV1beta1) Get(name string) (*batchv1.CronJob, error) {
	cj, err := a.l.Get(name)
	if err != nil {
		return nil, err
	}
	return cronJobFromV1beta1(cj), nil
}

// hpasFromV1 serves an autoscaling/v1 HPA cache through the autoscaling/v2
// lister interface, with what hpaFromV1 carries.
type hpasFromV1 struct {
	l autoscalingv1listers.HorizontalPodAutoscalerLister
}

func (a hpasFromV1) List(sel labels.Selector) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	items, err := a.l.List(sel)
	return convertAll(items, err, hpaFromV1)
}

func (a hpasFromV1) HorizontalPodAutoscalers(ns string) autoscalingv2listers.HorizontalPodAutoscalerNamespaceLister {
	return hpasInNamespaceFromV1{a.l.HorizontalPodAutoscalers(ns)}
}

type hpasInNamespaceFromV1 struct {
	l autoscalingv1listers.HorizontalPodAutoscalerNamespaceLister
}

func (a hpasInNamespaceFromV1) List(sel labels.Selector) ([]*autoscalingv2.HorizontalPodAutoscaler, error) {
	items, err := a.l.List(sel)
	return convertAll(items, err, hpaFromV1)
}

func (a hpasInNamespaceFromV1) Get(name string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	h, err := a.l.Get(name)
	if err != nil {
		return nil, err
	}
	return hpaFromV1(h), nil
}

// listCronJobs lists a namespace's CronJobs in whichever version the server
// serves: batch/v1, or batch/v1beta1 when v1 isn't there (a 404 on a List
// means the resource isn't served, never a missing object).
func listCronJobs(ctx context.Context, cs kubernetes.Interface, ns string) ([]batchv1.CronJob, error) {
	list, err := cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		return list.Items, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	old, err := cs.BatchV1beta1().CronJobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]batchv1.CronJob, len(old.Items))
	for i := range old.Items {
		out[i] = *cronJobFromV1beta1(&old.Items[i])
	}
	return out, nil
}
