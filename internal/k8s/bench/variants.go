// Package bench provides a structured benchmark harness for measuring
// Kubernetes API client performance against a fixture minikube cluster. See
// the plan at .claude/plans/create-a-plan-for-mighty-storm.md.
//
// The harness is read-only with respect to production code paths: it builds
// its own *rest.Config + kubernetes.Interface per variant, runs scenarios,
// and emits CSV + Markdown reports. Nothing in internal/k8s/*.go (outside
// this package) changes.
package bench

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// VariantID names one experimental cell. V0 is the baseline control.
type VariantID string

const (
	V0Baseline      VariantID = "V0-baseline"
	V1Protobuf      VariantID = "V1-protobuf"
	V2RV0List       VariantID = "V2-rv0-list"
	V3Paginated     VariantID = "V3-paginated"
	V4FieldSelector VariantID = "V4-field-selector"
	V5LabelSelector VariantID = "V5-label-selector"
	V6InformerCache VariantID = "V6-informer-cache"
	V7PartialMeta   VariantID = "V7-partial-metadata"
	V8Fanout4       VariantID = "V8-fanout-4"
	V9Fanout8       VariantID = "V9-fanout-8"
	V10Fanout16     VariantID = "V10-fanout-16"
	V11ComboBest    VariantID = "V11-combo-best"
	V12ConnPool     VariantID = "V12-conn-pool"
)

// AllVariants is the canonical ordered list used by the CLI when
// --variants=all. Order is informational; the runner randomizes execution.
func AllVariants() []VariantID {
	return []VariantID{
		V0Baseline, V1Protobuf, V2RV0List, V3Paginated,
		V4FieldSelector, V5LabelSelector, V6InformerCache, V7PartialMeta,
		V8Fanout4, V9Fanout8, V10Fanout16, V11ComboBest, V12ConnPool,
	}
}

// Options carries the per-variant knobs that scenarios consult when issuing
// List() calls. Each scenario is variant-agnostic — it asks the Options what
// ListOptions to use and how wide to fan out, then calls the API.
type Options struct {
	// ListOptions is the base options scenarios start from. Variants override
	// ResourceVersion (V2) and Limit (V3); scenarios may layer in field /
	// label selectors on top (V4 / V5 — controlled by the bool flags below).
	ListOptions metav1.ListOptions

	// FanoutLimit caps concurrent per-namespace goroutines. <=0 means unbounded
	// (current safina behavior — the V0 baseline).
	FanoutLimit int

	// UseFieldSelector tells scenarios to apply their canonical field selector
	// (e.g. status.phase=Running for hot pods). V4 only.
	UseFieldSelector bool

	// UseLabelSelector tells scenarios to apply their canonical label selector
	// where applicable. V5 only.
	UseLabelSelector bool

	// PaginatedContinue controls whether scenarios should loop on Continue
	// tokens when Limit is set. V3 sets both Limit and this flag.
	PaginatedContinue bool

	// Informer is non-nil iff the variant uses a shared informer cache. When
	// set, scenarios that have a cached path consult InformerPodLister
	// instead of issuing a List().
	Informer informers.SharedInformerFactory
	// PodLister is the cached lister derived from Informer. nil when Informer
	// is nil. Held here so scenarios don't re-derive it per iteration.
	PodLister corev1listers.PodLister

	// PreferPartialMetadata signals scenarios to use the metadata client for
	// list-only-the-names paths. V7 only.
	PreferPartialMetadata bool
}

// Variant is the closure form: given a base *rest.Config, build the
// kubernetes.Interface and Options to run with. The base config has already
// been wrapped with the bench MeteringTransport — the variant must NOT clone
// it in a way that drops the WrapTransport chain. Mutating the passed-in
// config in place is the supported pattern.
//
// Returned cleanup is invoked after every iteration of every scenario for
// this variant (e.g. stop informers, drop caches).
type Variant struct {
	ID    VariantID
	Build func(ctx context.Context, base *rest.Config) (cs kubernetes.Interface, opts *Options, cleanup func(), err error)
}

// Registry returns the full set of variants in a stable map for lookup. Use
// AllVariants() for ordered iteration.
func Registry() map[VariantID]Variant {
	return map[VariantID]Variant{
		V0Baseline:      {V0Baseline, buildBaseline},
		V1Protobuf:      {V1Protobuf, buildProtobuf},
		V2RV0List:       {V2RV0List, buildRV0},
		V3Paginated:     {V3Paginated, buildPaginated},
		V4FieldSelector: {V4FieldSelector, buildFieldSelector},
		V5LabelSelector: {V5LabelSelector, buildLabelSelector},
		V6InformerCache: {V6InformerCache, buildInformer},
		V7PartialMeta:   {V7PartialMeta, buildPartialMeta},
		V8Fanout4:       {V8Fanout4, buildFanout(4)},
		V9Fanout8:       {V9Fanout8, buildFanout(8)},
		V10Fanout16:     {V10Fanout16, buildFanout(16)},
		V11ComboBest:    {V11ComboBest, buildComboBest},
		V12ConnPool:     {V12ConnPool, buildConnPool},
	}
}

// Lookup resolves a variant by ID, returning an error rather than panicking
// on typos in --variants. The CLI surfaces this directly.
func Lookup(id VariantID) (Variant, error) {
	v, ok := Registry()[id]
	if !ok {
		return Variant{}, fmt.Errorf("unknown variant: %s", id)
	}
	return v, nil
}

// --- variant builders --------------------------------------------------------

func buildBaseline(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, &Options{}, func() {}, nil
}

func buildProtobuf(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	// Protobuf for cluster-scoped types; fallback to JSON for CRDs the
	// API server can't serve as protobuf. AcceptContentTypes preserves
	// fallback negotiation.
	base.ContentType = "application/vnd.kubernetes.protobuf"
	base.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, &Options{}, func() {}, nil
}

func buildRV0(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	// RV=0 reads from the apiserver watch cache, skipping etcd quorum reads.
	// Trades freshness (may see slightly stale data) for latency.
	return cs, &Options{ListOptions: metav1.ListOptions{ResourceVersion: "0"}}, func() {}, nil
}

func buildPaginated(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, &Options{
		ListOptions:       metav1.ListOptions{Limit: 500},
		PaginatedContinue: true,
	}, func() {}, nil
}

func buildFieldSelector(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, &Options{UseFieldSelector: true}, func() {}, nil
}

func buildLabelSelector(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	return cs, &Options{UseLabelSelector: true}, func() {}, nil
}

func buildInformer(ctx context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	// 0 resync = event-driven only, no periodic full resync — closest match
	// to "cache reads, never re-list unless we have to."
	factory := informers.NewSharedInformerFactory(cs, 0)
	podInformer := factory.Core().V1().Pods()
	lister := podInformer.Lister()
	stop := make(chan struct{})
	factory.Start(stop)
	// Wait for initial sync so the first iteration doesn't see an empty cache.
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), podInformer.Informer().HasSynced) {
		close(stop)
		return nil, nil, nil, fmt.Errorf("informer cache failed to sync within 30s")
	}
	opts := &Options{Informer: factory, PodLister: lister}
	return cs, opts, func() { close(stop) }, nil
}

func buildPartialMeta(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	// The metadata client is wired by scenarios that can use it; we just
	// signal intent here. Scenarios fall back to the typed client when they
	// need spec/status fields.
	return cs, &Options{PreferPartialMetadata: true}, func() {}, nil
}

func buildFanout(n int) func(context.Context, *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	return func(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
		cs, err := kubernetes.NewForConfig(base)
		if err != nil {
			return nil, nil, nil, err
		}
		return cs, &Options{FanoutLimit: n}, func() {}, nil
	}
}

func buildComboBest(ctx context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	// protobuf + RV=0 + informer — the stacked hypothesis we expect to win.
	base.ContentType = "application/vnd.kubernetes.protobuf"
	base.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	cs, opts, cleanup, err := buildInformer(ctx, base)
	if err != nil {
		return nil, nil, nil, err
	}
	opts.ListOptions = metav1.ListOptions{ResourceVersion: "0"}
	return cs, opts, cleanup, nil
}

// buildConnPool tunes the underlying *http.Transport's connection pool.
// Motivation: the full-grid run showed bimodal S3 latencies (clusters at
// 3-9ms and 99-104ms) with no variant materially affecting either mode.
//
// Status: this variant is INCONCLUSIVE. The reflection walk that locates the
// inner *http.Transport doesn't survive client-go's unexported auth-wrapper
// types (the WARNING fires when run). Even if it did, client-go's
// utilnet.SetTransportDefaults already sets MaxIdleConnsPerHost=25, well
// above the 5 parallel streams per iteration we issue — so the bimodality
// almost certainly has a different cause (HTTP/2 stream serialization,
// apiserver request queuing, watch-cache miss penalty).
//
// Kept here as a recorded null hypothesis. Fixing the mutation correctly
// requires either unsafe.Pointer-based field access or replacing the whole
// rest.Config.Transport, both of which are out of scope for the bench
// harness's read-only contract with production code.
func buildConnPool(_ context.Context, base *rest.Config) (kubernetes.Interface, *Options, func(), error) {
	tuned := false
	existing := base.WrapTransport
	base.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if existing != nil {
			rt = existing(rt)
		}
		tuned = tuneIdlePool(rt)
		return rt
	}
	cs, err := kubernetes.NewForConfig(base)
	if err != nil {
		return nil, nil, nil, err
	}
	if !tuned {
		// Surface so a silent reflection-walk failure can't masquerade as a
		// null result. Only fires once per variant build.
		fmt.Fprintln(os.Stderr, "V12-conn-pool: WARNING — tuneIdlePool did not find *http.Transport")
	}
	return cs, &Options{}, func() {}, nil
}

// tuneIdlePool walks an http.RoundTripper chain and bumps the innermost
// *http.Transport's idle-conn pool. Returns silently when the chain doesn't
// terminate in *http.Transport (which would be unusual for client-go). The
// bool return reports whether mutation happened — used by tests / smoke
// checks to confirm the reflection-based walk actually engaged.
func tuneIdlePool(rt http.RoundTripper) bool {
	for depth := 0; rt != nil && depth < 16; depth++ {
		if t, ok := rt.(*http.Transport); ok {
			t.MaxIdleConns = 200
			t.MaxIdleConnsPerHost = 64
			t.MaxConnsPerHost = 64
			return true
		}
		// Reflect into the struct, looking for an http.RoundTripper field
		// (the conventional wrapper pattern: type Foo struct{ Base RoundTripper }).
		v := reflect.ValueOf(rt)
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return false
		}
		var next http.RoundTripper
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				continue
			}
			if inner, ok := f.Interface().(http.RoundTripper); ok && inner != nil {
				next = inner
				break
			}
		}
		if next == nil {
			return false
		}
		rt = next
	}
	return false
}
