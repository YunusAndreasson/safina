package sections

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"k8s.io/apimachinery/pkg/types"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// NodeRef is the typed identifier for a level in the breadcrumb stack.
type NodeRef struct {
	Kind      string // k8s.KindRoot, KindApp, KindPod, KindContainer, KindNodePool, KindNode, KindSystemFold, KindNamespace
	SubKind   string // for KindApp: AppKindDeployment / StatefulSet / DaemonSet / CronJob / Job
	Namespace string
	Name      string
	UID       types.UID
	// Display is the label used when rendering the breadcrumb segment for
	// this ref (so we can render even when the live data has rotated out).
	Display string
	Health  k8s.Health
	// GVR encodes the resource's GroupVersionResource as "group/version/
	// resource" (group is empty for core) for KindCustomFold and
	// KindGenericResource refs. Empty for every other kind. Included in
	// pathKeyFor so two CRDs that happen to share a Kind name in
	// different groups get distinct cache entries.
	GVR string
}

// Children is the polymorphic payload for a single tree level. Exactly one
// typed slice is populated, matching `Kind`.
type Children struct {
	Kind       string
	Contexts   []config.ContextInfo // populated for KindContextList
	Apps       []k8s.AppInfo
	Pods       []k8s.PodInfo
	Containers []k8s.ContainerInfo
	Pools      []k8s.NodePoolInfo
	Nodes      []k8s.NodeInfo
	Namespaces []k8s.NamespaceInfo
	ConfigMaps []k8s.ConfigMapInfo
	Secrets    []k8s.SecretInfo
	Services   []k8s.ServiceInfo
	Ingresses  []k8s.IngressInfo
	PVCs       []k8s.PVCInfo
	PVs        []k8s.PVInfo
	// Generic carries instances of an enabled CRD/other GVR returned via
	// the dynamic client. Populated when Kind == KindGenericResource.
	Generic     []k8s.GenericResource
	SystemCount int                 // when > 0 in an Apps root, append a synthetic SystemFold row
	SystemNS    []string            // populated alongside SystemCount
	UserNS      []k8s.NamespaceInfo // user-namespace folders at cluster root (Kind == KindNamespaceFolder)
}

// ── Tree section ─────────────────────────────────────────────────────

// Tree is the breadcrumb-driven drill navigator that replaces the previous
// Deployments + Pods two-panel layout.
type Tree struct {
	Focused bool
	Loading bool

	// Refreshing is the "background reload in flight" flag — true while a
	// refresh (manual R or auto-tick) is fetching new data and we already
	// have stale rows on screen. Loading is the "first-time / no data yet"
	// flag and triggers a centered body spinner; Refreshing is quieter and
	// only puts a small spinner suffix in the panel title so the operator
	// knows data is updating without losing visual continuity. Cleared by
	// SetChildren / load error handler, same as Loading.
	Refreshing bool

	// refreshGate suppresses the title-spinner flicker on fast refreshes
	// (informer-cached reads finish in <50ms; without a gate the spinner
	// flashed in/out every auto-refresh tick at 5s cadence). Spinner only
	// appears when the load is slow enough to register perceptually.
	refreshGate components.LoadGate

	// loadGate gates the body spinner for cold loads so fast cache hits
	// don't flash a spinner during drill.
	loadGate components.LoadGate

	// LoadErr is the last error from the loader for the current focus.
	// Distinguishes "API call failed" from "API returned zero items" so the
	// renderer can show a retry hint in the failure case instead of the
	// indistinguishable empty placeholder. Cleared on the next successful
	// SetChildren or on a focus change (Push / Pop / ResetForContextSwitch).
	LoadErr error

	path []NodeRef

	// children is the loaded data for the current focus (path[-1]).
	children *Children

	// cursorByKey preserves selection across drill+pop.
	cursorByKey map[string]int

	// cache holds previously-loaded levels for instant Esc-back.
	cache map[string]*Children

	filter    components.Filter
	alphaSort bool

	cursor int

	// metricsHist keeps a rolling buffer of recent CPU/Memory samples for
	// each resource the tree has rendered, keyed as "kind/namespace/name".
	// The renderer reads these to draw the inline sparkline next to the
	// numeric value. Buffers grow over time as the root model re-pumps
	// children at the auto-refresh interval; on the very first render the
	// buffer has only one sample and the sparkline draws as a dim "─"
	// until the trend has enough body to show.
	metricsHist map[string]*metricSeries

	// nsHealthCache caches a namespace's app-health rollup keyed by name.
	// Populated whenever SetChildren delivers apps for a namespace; the
	// renderer reads it to draw the pip on namespace rows. A namespace
	// the user hasn't drilled into yet renders with a dim "?" pip until
	// the cache populates.
	nsHealthCache map[string]nsHealthSummary

	// healthPulse tracks resources whose health state recently flipped.
	// Renderers consult this to brighten / underline the row's health
	// glyph for ~pulseDuration after the change so the operator can spot
	// "this just went red" without scanning timestamps. Keyed by
	// "kind/namespace/name"; the value is the pulse start timestamp.
	healthPulse map[string]time.Time

	// lastHealth records the health value last seen for each resource
	// so SetChildren can detect a flip. Initial sightings don't pulse —
	// the operator only cares about transitions.
	lastHealth map[string]k8s.Health

	// enabledResources mirrors AppConfig.EnabledResources. Drives which
	// folds visibleRows() emits at the namespace / All level. The model
	// keeps this in sync via SetEnabledResources whenever the user
	// confirms a change in the Resource Browser.
	enabledResources []config.ResourceRef

	// discoveredKinds caches the result of ListAvailableResources for the
	// current cluster, keyed by "group/version/resource". Used to render
	// custom-fold labels with the real Kind name instead of the lowercase
	// plural Resource. Cleared on cluster switch.
	discoveredKinds map[string]k8s.ResourceKind
}

// nsHealthSummary aggregates the worst app-health state and a count for
// one namespace. Worst-of semantics match what an SRE expects from a
// roll-up — if any app is failing the indicator is red, even if 47
// others are healthy. Total is the app count for the optional badge.
type nsHealthSummary struct {
	worst k8s.Health
	total int
}

// pulseDuration is how long a row stays in the "just-changed-health"
// highlight after its health flipped. Chosen so a typical 5s auto-refresh
// covers two redraws of the highlight — long enough for the operator to
// notice the pulse without being a permanent visual nag.
const pulseDuration = 8 * time.Second

// metricSeries pairs CPU and Memory rolling buffers for a single resource.
// We keep them together so a single map lookup yields both.
type metricSeries struct {
	cpu *components.MetricBuffer
	mem *components.MetricBuffer
}

// sparkWidth is the cell count for inline metric sparklines. Four gives
// enough body to read a trend without bloating the column on narrow
// terminals; the numeric value sits to the right so the operator still
// gets the absolute reading at a glance.
const sparkWidth = 4

// metricsHistCap is the per-resource sample retention. At a 5s auto-refresh
// cadence (refreshInterval), this buffer length covers ~6 hours of history
// when a resource stays in view continuously. The tree row's narrow
// sparkline still reads only the most recent samples (so scan latency is
// unaffected), while the Inspector's wide sparkline downsamples the full
// buffer via max-bucket aggregation to expose long-term shape on the
// right pane. Per-resource memory: ~6h × 2 series × 8B ≈ 70KB.
const metricsHistCap = 4320

// Domain-aware floor ceilings for the rolling buffers. Picked so a pod
// stable at 1 millicore / 2 MiB renders as a near-zero floor line in
// gray instead of as max-height red bars (the bug the user surfaced):
//   - cpuCeilingFloor: 1 core = 1000 millicores. A single core is the
//     natural "100%" reference for a generic pod; the buffer ratchets up
//     in 2× steps (2000m, 4000m, ...) when observed usage exceeds it.
//   - memCeilingFloor: 256 MiB. Most application pods sit comfortably
//     under this; bursty workloads ratchet to 512 MiB / 1 GiB / 2 GiB
//     automatically.
//
// These are deliberately *not* the same as k8s `resources.limits` —
// fetching limits would require a per-pod GET, which the tree's batch
// list doesn't carry today. The floor approach gets the user 95% of the
// way for ~0 code; threading real limits through PodInfo is a follow-up.
const (
	cpuCeilingFloor int64 = 1000
	memCeilingFloor int64 = 256 * 1024 * 1024
)

// NewTree builds an empty Tree rooted at the unified root.
func NewTree(c *tuictx.Ctx) Tree {
	t := Tree{
		cursorByKey:      make(map[string]int),
		cache:            make(map[string]*Children),
		metricsHist:      make(map[string]*metricSeries),
		nsHealthCache:    make(map[string]nsHealthSummary),
		healthPulse:      make(map[string]time.Time),
		lastHealth:       make(map[string]k8s.Health),
		filter:           components.NewFilter(c.Theme),
		enabledResources: config.DefaultEnabledResources(),
		discoveredKinds:  make(map[string]k8s.ResourceKind),
	}
	t.path = []NodeRef{rootRef()}
	// Filter the title-spinner for fast refreshes that hit the cache;
	// only genuinely slow ones (>600ms) surface.
	t.refreshGate.ShowAfter = 600 * time.Millisecond
	t.refreshGate.MinVisible = 200 * time.Millisecond
	return t
}

// SetEnabledResources updates the fold list driving namespace-level
// rendering. Called by the root model after Load (boot) and after the
// Resource Browser confirms a selection.
func (t *Tree) SetEnabledResources(refs []config.ResourceRef) {
	t.enabledResources = append([]config.ResourceRef(nil), refs...)
}

// EnabledResources returns the current fold list. The Resource Browser
// reads it to seed its initial "what's checked" state.
func (t *Tree) EnabledResources() []config.ResourceRef {
	return append([]config.ResourceRef(nil), t.enabledResources...)
}

// SetDiscoveredKinds replaces the discovery cache used to resolve custom-
// fold display labels. Indexed by GVR string ("group/version/resource").
func (t *Tree) SetDiscoveredKinds(kinds []k8s.ResourceKind) {
	t.discoveredKinds = make(map[string]k8s.ResourceKind, len(kinds))
	for _, k := range kinds {
		t.discoveredKinds[gvrKey(k.Group, k.Version, k.Resource)] = k
	}
}

// DiscoveredKinds returns the cached discovery result. Empty until the
// first ListAvailableResources call lands. The Resource Browser uses it
// to populate the "all kinds" list.
func (t *Tree) DiscoveredKinds() []k8s.ResourceKind {
	out := make([]k8s.ResourceKind, 0, len(t.discoveredKinds))
	for _, k := range t.discoveredKinds {
		out = append(out, k)
	}
	return out
}

// gvrKey is the canonical string form of a GVR — what NodeRef.GVR and
// the discoveredKinds map both use. Group is allowed to be empty.
func gvrKey(group, version, resource string) string {
	return group + "/" + version + "/" + resource
}

// foldSpec is one fold row at the namespace level — synthesized from a
// ResourceRef + the tree's discovery cache. Kind is the NodeRef.Kind the
// fold will emit; Label is the display name (plural); GVR is the encoded
// "group/version/resource" used when Kind == KindCustomFold. countKind is
// the cache key for foldDisplay's count lookup.
type foldSpec struct {
	Kind      string
	Label     string
	GVR       string
	CountKind string // matches Kind for cache lookup
}

// foldSpecsFor expands a list of ResourceRefs into the fold rows the
// tree should render. Built-in defaults map back to the existing fold
// Kinds (KindConfigFold, KindSecretsFold, …) so the existing typed-
// clientset list paths keep working. Everything else becomes a
// KindCustomFold that drills via the dynamic client.
//
// Custom folds whose GVR isn't in the current cluster's discovery
// cache are dropped — drilling them would 404, and the user just
// switched to a cluster that doesn't have that CRD installed. The
// underlying config stays intact: switching back to a cluster that
// does have it re-surfaces the fold automatically. Built-in defaults
// (configmaps/secrets/…) are always emitted; they exist on every
// conformant cluster, and skipping the discovery check there avoids
// an awkward gap during the brief window before discovery lands.
func (t *Tree) foldSpecsFor(refs []config.ResourceRef) []foldSpec {
	out := make([]foldSpec, 0, len(refs))
	for _, r := range refs {
		spec, ok := t.builtInFold(r)
		if ok {
			out = append(out, spec)
			continue
		}
		if !t.kindDiscovered(r) {
			continue
		}
		out = append(out, t.customFold(r))
	}
	return out
}

// kindDiscovered reports whether a non-default ResourceRef appears in
// the current cluster's discovery cache. Returns true when the cache
// hasn't been populated yet (so the fold renders by default during the
// startup gap) — once discovery lands, the gate becomes strict.
func (t *Tree) kindDiscovered(r config.ResourceRef) bool {
	if len(t.discoveredKinds) == 0 {
		return true
	}
	_, ok := t.discoveredKinds[gvrKey(r.Group, r.Version, r.Resource)]
	return ok
}

// builtInFold returns the fold spec for the 6 historical defaults so the
// tree keeps using the typed-clientset paths for them (faster, richer
// row data). Returns ok=false for anything else.
func (t *Tree) builtInFold(r config.ResourceRef) (foldSpec, bool) {
	if r.Synthetic == "Workloads" {
		return foldSpec{Kind: k8s.KindWorkloadsFold, Label: "Workloads", CountKind: "WorkloadsFold"}, true
	}
	switch {
	case r.Group == "" && r.Resource == "configmaps":
		return foldSpec{Kind: k8s.KindConfigFold, Label: "ConfigMaps", CountKind: "ConfigFold"}, true
	case r.Group == "" && r.Resource == "secrets":
		return foldSpec{Kind: k8s.KindSecretsFold, Label: "Secrets", CountKind: "SecretsFold"}, true
	case r.Group == "" && r.Resource == "services":
		return foldSpec{Kind: k8s.KindNetworkFold, Label: "Services", CountKind: "NetworkFold"}, true
	case r.Group == "networking.k8s.io" && r.Resource == "ingresses":
		return foldSpec{Kind: k8s.KindIngressFold, Label: "Ingresses", CountKind: "IngressFold"}, true
	case r.Group == "" && r.Resource == "persistentvolumeclaims":
		return foldSpec{Kind: k8s.KindStorageFold, Label: "PVCs", CountKind: "StorageFold"}, true
	}
	return foldSpec{}, false
}

// customFold builds a KindCustomFold spec for a non-default ResourceRef.
// Resolves the display label from the discovery cache (Kind name) and
// falls back to a title-cased Resource when discovery hasn't populated
// yet.
func (t *Tree) customFold(r config.ResourceRef) foldSpec {
	gvr := gvrKey(r.Group, r.Version, r.Resource)
	label := titleCasePlural(r.Resource)
	if rk, ok := t.discoveredKinds[gvr]; ok && rk.Kind != "" {
		label = kindNameAsPlural(rk.Kind)
	}
	return foldSpec{Kind: k8s.KindCustomFold, Label: label, GVR: gvr, CountKind: gvr}
}

// titleCasePlural is the fallback when the discovery cache hasn't
// populated yet — uppercase the first rune of the plural resource name
// so "prometheusrules" reads as "Prometheusrules". Not as good as the
// real Kind name but works without a network call.
func titleCasePlural(resource string) string {
	if resource == "" {
		return resource
	}
	first := strings.ToUpper(resource[:1])
	return first + resource[1:]
}

// kindNameAsPlural turns a singular Kind name into a display plural. Not
// a full inflection engine — handles the patterns that cover the
// overwhelming majority of K8s Kind names. Edge cases that don't match
// (e.g. "Endpoints" which is already plural) round-trip to "Endpoints"
// because they already end in "s".
func kindNameAsPlural(kind string) string {
	if kind == "" {
		return kind
	}
	switch {
	case strings.HasSuffix(kind, "s"):
		return kind
	case strings.HasSuffix(kind, "y"):
		return kind[:len(kind)-1] + "ies"
	case strings.HasSuffix(kind, "x"), strings.HasSuffix(kind, "ch"), strings.HasSuffix(kind, "sh"):
		return kind + "es"
	}
	return kind + "s"
}

// foldDisplay builds the fold label "Name (N)" from the cached count, or
// the bare label when the fold hasn't been drilled yet — emitting "(?)"
// for every unvisited fold made the screen look broken on a fresh cluster.
// gvr is included in the path key so CRDs that share a Kind name in
// different groups don't collide.
func (t *Tree) foldDisplay(label, foldKind, gvr string) string {
	contextPath := t.path
	childPath := append([]NodeRef(nil), contextPath...)
	childPath = append(childPath, NodeRef{Kind: foldKind, Name: label, Display: label, GVR: gvr})
	ch := t.cache[pathKeyFor(childPath)]
	if ch == nil {
		return label
	}
	n := len(ch.ConfigMaps) + len(ch.Secrets) + len(ch.Services) + len(ch.Ingresses) + len(ch.PVCs) + len(ch.Pools) + len(ch.Generic)
	return fmt.Sprintf("%s (%d)", label, n)
}

// recordHealthPulse compares a resource's new health against its last
// known value. If they differ AND we've seen the resource before, a
// pulse is recorded; first sightings are silent so the screen doesn't
// light up the moment the user opens a namespace.
func (t *Tree) recordHealthPulse(kind, namespace, name string, h k8s.Health) {
	k := kind + "/" + namespace + "/" + name
	prev, seen := t.lastHealth[k]
	t.lastHealth[k] = h
	if !seen || prev == h {
		return
	}
	t.healthPulse[k] = time.Now()
}

// isPulsing reports whether a resource's health glyph should currently
// render with the pulse-highlight style. Returns false once the pulse
// has decayed past pulseDuration; callers don't need to clean up the
// map themselves — entries simply go stale.
func (t *Tree) isPulsing(kind, namespace, name string) bool {
	ts, ok := t.healthPulse[kind+"/"+namespace+"/"+name]
	if !ok {
		return false
	}
	return time.Since(ts) < pulseDuration
}

// metricKey is the rolling-buffer map key for a resource's metric series.
func metricKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

// pushMetricSamples records the latest CPU/Memory point for a resource
// into the rolling buffers. Called from SetChildren whenever a fresh load
// lands so the sparkline catches up to "now". Zero-valued samples still
// push — a zero is a real reading and reads as a dip in the trend.
func (t *Tree) pushMetricSamples(kind, namespace, name string, cpu, mem int64) {
	k := metricKey(kind, namespace, name)
	s, ok := t.metricsHist[k]
	if !ok {
		s = &metricSeries{
			cpu: components.NewMetricBufferWithFloor(metricsHistCap, cpuCeilingFloor),
			mem: components.NewMetricBufferWithFloor(metricsHistCap, memCeilingFloor),
		}
		t.metricsHist[k] = s
	}
	s.cpu.Push(cpu)
	s.mem.Push(mem)
}

// metricSeriesFor returns the rolling buffers for a resource if any have
// been recorded. Returns nil pair when the renderer has nothing to draw.
func (t *Tree) metricSeriesFor(kind, namespace, name string) (*components.MetricBuffer, *components.MetricBuffer) {
	s, ok := t.metricsHist[metricKey(kind, namespace, name)]
	if !ok {
		return nil, nil
	}
	return s.cpu, s.mem
}

// StartRefresh marks a background reload as in flight and arms the gate so
// the title spinner only appears for slow loads. Idempotent.
func (t *Tree) StartRefresh() {
	t.Refreshing = true
	t.refreshGate.Start()
}

// FinishRefresh clears the in-flight flag and releases the gate. Safe to
// call without a matching StartRefresh.
func (t *Tree) FinishRefresh() {
	t.Refreshing = false
	t.refreshGate.Stop()
}

// StartLoad arms the body-load gate so a fast cold load completes
// without ever painting the centered spinner. Pair with FinishLoad on
// success or error path. Idempotent — repeating Start re-anchors the
// gate's clock to "now".
func (t *Tree) StartLoad() {
	t.loadGate.Start()
}

// FinishLoad releases the body-load gate. Safe to call without a
// matching StartLoad (no-op).
func (t *Tree) FinishLoad() {
	t.loadGate.Stop()
}

// MetricSeriesFor is the exported counterpart used by the Inspector tile so
// it can render the same sparkline the row already shows. Returns nil pair
// when there's no history yet (the tile then skips the metric block).
func (t *Tree) MetricSeriesFor(kind, namespace, name string) (*components.MetricBuffer, *components.MetricBuffer) {
	return t.metricSeriesFor(kind, namespace, name)
}

// Cache exposes the level cache to the command palette indexer. The
// palette walks every cached level and builds a flat search corpus.
// Returned map is the live store — callers must not mutate it.
func (t *Tree) Cache() map[string]*Children { return t.cache }

// NavigateTo replaces the current path with the given one and clears
// transient view state (filter, cursor) so the user lands on the
// selected entry. Emits a TreeFocusChangedMsg so the root model issues
// a load if the destination level isn't already cached.
//
// Used by the command palette: the user picks a far-away resource via
// fuzzy match and we drill straight there instead of forcing them to
// pop back up and down through the tree.
func (t *Tree) NavigateTo(path []NodeRef) tea.Cmd {
	if len(path) == 0 {
		return nil
	}
	// Save current cursor before swapping paths.
	t.cursorByKey[t.pathKey()] = t.cursor
	t.path = clonePath(path)
	t.children = t.cache[t.pathKey()]
	t.cursor = t.cursorByKey[t.pathKey()]
	t.LoadErr = nil
	t.filter.Clear()
	return emit(TreeFocusChangedMsg{Path: clonePath(t.path)})
}

// SetCursorByRef positions the cursor on a row matching the supplied
// kind/namespace/name within the current visible rows. No-op when no
// match is found. Used after NavigateTo when the destination's parent
// level is already loaded so the cursor lands on the picked entry
// rather than the top of the list.
func (t *Tree) SetCursorByRef(kind, namespace, name string) {
	for i, r := range t.visibleRows() {
		if r.Kind == kind && r.Namespace == namespace && r.Name == name {
			t.cursor = i
			return
		}
	}
}

func rootRef() NodeRef {
	return NodeRef{
		Kind:    k8s.KindContextList,
		Name:    "Clusters",
		Display: "Clusters",
	}
}

// SeedContexts pre-fills the cache for the root level with the static list
// of kube contexts. The context list isn't a k8s API call — we have it in
// hand from kubeconfig — so there's no need to round-trip through a loader.
func (t *Tree) SeedContexts(items []config.ContextInfo) {
	rootKey := pathKeyFor([]NodeRef{rootRef()})
	t.cache[rootKey] = &Children{Kind: k8s.KindContextList, Contexts: items}
	if t.pathKey() == rootKey {
		t.children = t.cache[rootKey]
	}
}

// PushContext drills the tree one level into the named context. Used by the
// root when the user picks a cluster from the picker.
func (t *Tree) PushContext(name string) tea.Cmd {
	return t.Push(NodeRef{
		Kind:    k8s.KindContext,
		Name:    name,
		Display: name,
	})
}

// pathKey serializes the path stack to a stable string for cache + cursor.
func (t *Tree) pathKey() string { return pathKeyFor(t.path) }

func pathKeyFor(p []NodeRef) string {
	parts := make([]string, len(p))
	for i, r := range p {
		parts[i] = r.Kind + "/" + r.Namespace + "/" + r.Name + "/" + string(r.UID) + "/" + r.GVR
	}
	return strings.Join(parts, "|")
}

// Path returns the current breadcrumb path (read-only).
func (t *Tree) Path() []NodeRef { return t.path }

// FilterActive reports whether the tree's filter input is currently
// receiving keystrokes. The root checks this before consuming letter keys
// (e.g. `i` for Ask) so the user can type literal letters into the filter
// without accidentally jumping into the chat panel.
func (t *Tree) FilterActive() bool { return t.filter.Active() }

// FilterValue exposes the current filter query string — non-empty when a
// committed filter is still narrowing the rows (input closed but value
// sticky). Used by the root dispatch so Esc clears a sticky filter
// before falling through to a higher-level "back out" action.
func (t *Tree) FilterValue() string { return t.filter.Value() }

// YankPayload returns the kubectl-pasteable identifier for the currently
// focused row — empty if there is nothing selectable. We bias toward
// "<namespace>/<name>" for namespaced resources because that's unambiguous
// when copied into a fresh terminal where the operator hasn't already
// scoped to a namespace. Cluster-level rows return their bare name.
func (t *Tree) YankPayload() string {
	ref := t.SelectedRef()
	switch ref.Kind {
	case k8s.KindContext, k8s.KindNode, k8s.KindNodePool, k8s.KindNamespace, k8s.KindNamespaceFolder:
		return ref.Name
	case k8s.KindApp, k8s.KindPod, k8s.KindService, k8s.KindConfigMap, k8s.KindIngress,
		k8s.KindSecret, k8s.KindPVC:
		if ref.Namespace == "" {
			return ref.Name
		}
		return ref.Namespace + "/" + ref.Name
	case k8s.KindContainer:
		// The container's Namespace is the parent pod's; Name is the
		// container itself. Surface both so a paste like `kubectl exec -it
		// <ns>/<pod> -c <container>` can be assembled at a glance — we
		// don't have the pod's name on the ref alone, so fall back to the
		// breadcrumb path if it's available.
		if len(t.path) >= 2 {
			pod := t.path[len(t.path)-1]
			if pod.Kind == k8s.KindPod && pod.Namespace != "" {
				return pod.Namespace + "/" + pod.Name + "/" + ref.Name
			}
		}
		return ref.Name
	}
	return ""
}

// Focus is the current parent ref (path[-1]).
func (t *Tree) Focus() NodeRef {
	if len(t.path) == 0 {
		return rootRef()
	}
	return t.path[len(t.path)-1]
}

// Children returns the loaded children for the current focus, or nil while
// loading or unloaded. Callers should also check Tree.Loading.
func (t *Tree) Children() *Children { return t.children }

// SelectedRef returns the NodeRef of the highlighted child, or zero value
// when the level is empty.
func (t *Tree) SelectedRef() NodeRef {
	rows := t.visibleRows()
	if t.cursor < 0 || t.cursor >= len(rows) {
		return NodeRef{}
	}
	return rows[t.cursor]
}

// SelectedAppInfo returns the highlighted AppInfo and true when the focus
// renders apps; otherwise zero value and false. (Convenience for the action
// dispatcher in the root model.)
func (t *Tree) SelectedAppInfo() (k8s.AppInfo, bool) {
	if t.children == nil || t.children.Kind != k8s.KindApp {
		return k8s.AppInfo{}, false
	}
	apps := t.filteredApps()
	if t.cursor < 0 || t.cursor >= len(apps) {
		return k8s.AppInfo{}, false
	}
	return apps[t.cursor], true
}

// SelectedPodInfo returns the highlighted PodInfo when the focus renders
// pods; otherwise zero value and false.
func (t *Tree) SelectedPodInfo() (k8s.PodInfo, bool) {
	if t.children == nil || t.children.Kind != k8s.KindPod {
		return k8s.PodInfo{}, false
	}
	pods := t.filteredPods()
	if t.cursor < 0 || t.cursor >= len(pods) {
		return k8s.PodInfo{}, false
	}
	return pods[t.cursor], true
}

// SelectedNodeInfo returns the highlighted NodeInfo when the focus renders
// nodes; otherwise zero value and false. The cordon/drain dispatchers read
// the Schedulable bit off this so the C-toggle picks cordon vs uncordon
// without an extra API round-trip.
func (t *Tree) SelectedNodeInfo() (k8s.NodeInfo, bool) {
	if t.children == nil || t.children.Kind != k8s.KindNode {
		return k8s.NodeInfo{}, false
	}
	nodes := t.filteredNodes()
	if t.cursor < 0 || t.cursor >= len(nodes) {
		return k8s.NodeInfo{}, false
	}
	return nodes[t.cursor], true
}

// SetChildren stores the loaded payload for the given path and updates the
// current view if that path is still the focus.
//
// Cursor preservation:
//   - On a refresh (auto-tick or manual `R`), keep the user on the same row
//     by name: capture the highlighted ref before swapping children, then
//     relocate it in the new list.
//   - On first arrival after a Push (no prior children), use the cursor
//     value Push seeded from cursorByKey.
//   - When the named row is gone, clamp the existing cursor into range
//     instead of jumping to 0 — that way "next neighbour" is still in view
//     after a delete/scale-down.
func (t *Tree) SetChildren(path []NodeRef, ch *Children) {
	key := pathKeyFor(path)
	t.cache[key] = ch
	// Ingest any metric-bearing rows into the rolling history before the
	// rest of SetChildren runs — even if this load was for a non-current
	// path, the samples are still accurate and worth keeping. The history
	// is a flat map; cardinality is bounded by the number of distinct
	// resources the user has visited in the session.
	if ch != nil {
		for _, a := range ch.Apps {
			t.pushMetricSamples(k8s.KindApp, a.Namespace, a.Name, a.CPU, a.Memory)
			t.recordHealthPulse(k8s.KindApp, a.Namespace, a.Name, a.Health)
		}
		for _, p := range ch.Pods {
			t.pushMetricSamples(k8s.KindPod, p.Namespace, p.Name, p.CPU, p.Memory)
			t.recordHealthPulse(k8s.KindPod, p.Namespace, p.Name, k8s.PodHealth(p))
		}
		for _, c := range ch.Containers {
			t.pushMetricSamples(k8s.KindContainer, c.Namespace, c.PodName+"/"+c.Name, c.CPU, c.Memory)
		}
		// Nodes and pools encode utilization as per-mille of allocatable so the
		// sparkline's 0-1000 range maps to 0-100% of capacity — same gradient
		// thresholds (green/yellow/red) line up with the percentage text the
		// row already shows. Overcommit beyond 100% ratchets the buffer to
		// 2000, which gracefully degrades instead of clipping.
		for _, n := range ch.Nodes {
			t.pushMetricSamples(k8s.KindNode, "", n.Name,
				perMille(n.CPUUsed, n.CPUAlloc),
				perMille(n.MemUsed, n.MemAlloc))
			t.recordHealthPulse(k8s.KindNode, "", n.Name, n.Health)
		}
		for _, p := range ch.Pools {
			t.pushMetricSamples(k8s.KindNodePool, "", p.Name,
				perMille(p.CPUUsed, p.CPUAlloc),
				perMille(p.MemUsed, p.MemAlloc))
			t.recordHealthPulse(k8s.KindNodePool, "", p.Name, p.Health)
		}
		// Namespace health rollup: when a level delivers Apps, group
		// them by namespace and record a worst-of summary per namespace.
		// Skipping this when no apps are present keeps a stale pip from
		// being clobbered by an unrelated load (e.g. listing PVCs).
		if len(ch.Apps) > 0 {
			perNS := make(map[string]nsHealthSummary, 4)
			for _, a := range ch.Apps {
				s, ok := perNS[a.Namespace]
				if !ok {
					s = nsHealthSummary{worst: k8s.HealthHealthy}
				}
				s.worst = s.worst.Worse(a.Health)
				s.total++
				perNS[a.Namespace] = s
			}
			for ns, s := range perNS {
				t.nsHealthCache[ns] = s
			}
		}
	}
	if key != t.pathKey() {
		return
	}

	t.LoadErr = nil

	prev := t.SelectedRef()
	t.children = ch

	if prev.Kind != "" {
		for i, r := range t.visibleRows() {
			if r.Kind == prev.Kind && r.Name == prev.Name && r.Namespace == prev.Namespace {
				t.cursor = i
				return
			}
		}
	}

	n := t.visibleCount()
	if n == 0 {
		t.cursor = 0
		return
	}
	if t.cursor >= n {
		t.cursor = n - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// Push drills into the given child. Returns a TreeFocusChangedMsg cmd so the
// root model can issue a load. The new level is empty until SetChildren
// arrives.
func (t *Tree) Push(ref NodeRef) tea.Cmd {
	t.cursorByKey[t.pathKey()] = t.cursor
	t.path = append(t.path, ref)
	t.children = t.cache[t.pathKey()]
	t.cursor = t.cursorByKey[t.pathKey()]
	t.LoadErr = nil
	t.filter.Clear()
	return emit(TreeFocusChangedMsg{Path: clonePath(t.path)})
}

// Pop returns to the parent level. Cursor for the popped level is preserved.
func (t *Tree) Pop() tea.Cmd {
	if len(t.path) <= 1 {
		return nil // already at root
	}
	t.cursorByKey[t.pathKey()] = t.cursor
	t.path = t.path[:len(t.path)-1]
	t.children = t.cache[t.pathKey()]
	t.cursor = t.cursorByKey[t.pathKey()]
	t.LoadErr = nil
	t.filter.Clear()
	return emit(TreeFocusChangedMsg{Path: clonePath(t.path)})
}

// InvalidateFocus drops the cache entry for the current focus so the next
// load fetches fresh data. Called after a successful action (scale, etc.).
//
// The context-list root is *seeded* from kubeconfig (not loaded from k8s)
// and is immutable for the lifetime of the program — invalidating it would
// just blank the picker, since the no-op loader for KindContextList returns
// empty children. Skip it so a stray ActionDoneMsg landing while the user
// is back at the picker can't wipe the seeded list.
func (t *Tree) InvalidateFocus() {
	if t.Focus().Kind == k8s.KindContextList {
		return
	}
	delete(t.cache, t.pathKey())
	if len(t.path) > 2 {
		parent := pathKeyFor(t.path[:len(t.path)-1])
		delete(t.cache, parent)
	}
}

// ResetForContextSwitch drops every cached level *below* the context-list
// root, so the next drill into any context fetches fresh data. The root
// (KindContextList) cache survives — its content is the kubeconfig contexts
// list, which doesn't change at runtime.
func (t *Tree) ResetForContextSwitch() {
	rootKey := pathKeyFor([]NodeRef{rootRef()})
	rootCache := t.cache[rootKey]
	t.cache = make(map[string]*Children)
	if rootCache != nil {
		t.cache[rootKey] = rootCache
	}
	t.cursorByKey = make(map[string]int)
	t.filter.Clear()
}

// ── Update ───────────────────────────────────────────────────────────

func (t *Tree) Update(msg tea.Msg, c *tuictx.Ctx) tea.Cmd {
	if !t.Focused {
		return nil
	}
	if paste, ok := msg.(tea.PasteMsg); ok && t.filter.Active() {
		t.cursor = 0
		return t.filter.Update(paste)
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if t.filter.Active() {
		return t.updateFiltering(km)
	}

	switch {
	case key.Matches(km, c.Keys.Filter):
		t.filter.Open()
		return nil
	case key.Matches(km, c.Keys.Up):
		t.cursor = clamp(t.cursor-1, 0, t.visibleCount()-1)
	case key.Matches(km, c.Keys.Down):
		t.cursor = clamp(t.cursor+1, 0, t.visibleCount()-1)
	case km.String() == "g":
		t.cursor = 0
	case km.String() == "G":
		if n := t.visibleCount(); n > 0 {
			t.cursor = n - 1
		}
	case km.String() == "o":
		t.alphaSort = !t.alphaSort
	case key.Matches(km, c.Keys.FirstFail):
		t.cursor = t.firstUnhealthyIndex()
	case key.Matches(km, c.Keys.Enter):
		ref := t.SelectedRef()
		if ref.Kind == "" {
			return nil
		}
		// Container is terminal — no further drill. Logs are bound to `l`.
		if ref.Kind == k8s.KindContainer {
			return nil
		}
		return t.Push(ref)
	case key.Matches(km, c.Keys.Back):
		// If a filter query is sticky (input closed but value present), clear
		// before popping.
		if t.filter.Value() != "" {
			t.filter.Clear()
			t.cursor = 0
			return nil
		}
		if len(t.path) > 1 {
			return t.Pop()
		}
	}
	return nil
}

func (t *Tree) updateFiltering(km tea.KeyPressMsg) tea.Cmd {
	switch km.String() {
	case "esc":
		t.filter.Cancel()
		t.cursor = 0
		return nil
	case "enter":
		t.filter.Close()
		return nil
	}
	cmd := t.filter.Update(km)
	t.cursor = 0
	return cmd
}

// ── Render ───────────────────────────────────────────────────────────

func (t *Tree) View(c *tuictx.Ctx, w, h int) string {
	th := c.Theme

	// Title is a stable "Resource" label. The breadcrumb path lives in the
	// topbar — that's where users instinctively look for "where am I", so
	// putting it at the top of the screen frees this panel header from
	// repeating itself on every drill.
	innerW := components.PanelContentWidth(w)
	title := t.PanelTitle()
	// Background-refresh feedback: while a reload is in flight (manual R
	// or auto-tick) we keep the existing rows on screen and append a
	// small spinner to the panel title — the operator sees "data is
	// updating" without losing context. The PanelWithAccent loader-state
	// branch (Loading=true && children==nil) still owns the empty-screen
	// case; this is for the "we have data, refreshing it" case.
	if t.refreshGate.Visible() && t.children != nil {
		title = title + " " + c.Spinner.View()
	}

	// Title goes Dim, not cyan-bold — it's a label, not a headline. The
	// loudest text on this panel should be the data rows, and the loudest
	// chrome accent should be the cyan border (focus marker). A cyan title
	// stacked under the cyan logo in the topbar made both compete; pulling
	// the title down to Dim reorders the hierarchy so the eye flows
	// logo → breadcrumb → rows without the title interrupting.
	render := func(content string) string {
		if t.filter.Active() {
			return components.PanelWithAccent(th, title, w, h, content, t.Focused, th.P.Pink, th.P.Dim)
		}
		return components.PanelWithAccent(th, title, w, h, content, t.Focused, th.P.Cyan, th.P.Dim)
	}

	// Reserved rows: col header (1) + optional filter (1).
	reserved := 1
	if t.filter.Active() {
		reserved++
	}
	rowsAvail := h - 2 - reserved
	if rowsAvail < 1 {
		rowsAvail = 1
	}

	body := t.renderBody(c, innerW, rowsAvail)

	parts := []string{}
	if t.filter.Active() {
		// Pin the filter input to the panel's content width so its trailing
		// boundary doesn't shift between renders. Without this, the textinput
		// emits a string sized to its content, and the diff renderer may leave
		// stale cells (border glyphs) past the input's right edge after the
		// filter closes.
		t.filter.SetWidth(innerW)
		parts = append(parts, t.filter.View())
	}
	parts = append(parts, body)
	return render(strings.Join(parts, "\n"))
}

// BreadcrumbCrumbs returns the current path as a slice of components.Crumb
// for the topbar's location strip. We deliberately omit kind glyphs here:
// breadcrumb segments are already named ("prod-east-1" reads as a cluster,
// "my-app" as an app) so a leading glyph adds visual noise without adding
// information. Glyphs earn their place in *row* lists where many similar
// names compete and a 1-cell kind cue speeds scanning — the topbar is a
// short path of distinct, self-labelling names.
func (t *Tree) BreadcrumbCrumbs() []components.Crumb {
	out := make([]components.Crumb, len(t.path))
	for i, r := range t.path {
		out[i] = components.Crumb{Label: r.Display, Pinned: r.Kind == k8s.KindContext}
	}
	return out
}

// PanelTitle returns the kind label for the current view — "Clusters",
// "Apps", "Pods", "Containers", and so on. This is the answer to "what
// kind am I looking at"; the topbar breadcrumb answers "how did I get
// here". The row count is left to the data itself — counting is the
// reader's job, not the chrome's.
func (t *Tree) PanelTitle() string {
	if t.children != nil {
		switch t.children.Kind {
		case k8s.KindContextList:
			return "Clusters"
		case k8s.KindNamespaceFolder:
			return "Namespaces"
		case k8s.KindApp:
			// Inside a namespace folder or the All folder, the panel lists
			// scoped resource folds — not apps directly. Label accordingly.
			focusKind := t.Focus().Kind
			if t.filter.Value() == "" && (focusKind == k8s.KindAllNamespacesFold ||
				focusKind == k8s.KindNamespaceFolder ||
				focusKind == k8s.KindNamespace) {
				return "Resources"
			}
			return "Workloads"
		case k8s.KindPod:
			return "Pods"
		case k8s.KindContainer:
			return "Containers"
		case k8s.KindNodePool:
			return "Node Pools"
		case k8s.KindNode:
			return "Nodes"
		case k8s.KindNamespace:
			return "Namespaces"
		case k8s.KindConfigMap:
			return "ConfigMaps"
		case k8s.KindSecret:
			return "Secrets"
		case k8s.KindService:
			return "Services"
		case k8s.KindIngress:
			return "Ingresses"
		case k8s.KindPVC:
			return "PVCs"
		case k8s.KindPV:
			return "PersistentVolumes"
		}
	}
	return childKindLabel(t.Focus().Kind)
}

// childKindLabel maps a focus kind to the plural noun for what its children
// will be — used as the panel title while a level is still loading.
func childKindLabel(focusKind string) string {
	switch focusKind {
	case k8s.KindContextList:
		return "Clusters"
	case k8s.KindContext:
		return "Namespaces"
	case k8s.KindAllNamespacesFold, k8s.KindNamespaceFolder, k8s.KindNamespace:
		return "Resources"
	case k8s.KindApp:
		return "Pods"
	case k8s.KindPod:
		return "Containers"
	case k8s.KindClusterFold:
		return "Node Pools"
	case k8s.KindNodePool, k8s.KindNode:
		return "Nodes"
	case k8s.KindSystemFold:
		return "Namespaces"
	case k8s.KindWorkloadsFold:
		return "Workloads"
	case k8s.KindConfigFold:
		return "ConfigMaps"
	case k8s.KindSecretsFold:
		return "Secrets"
	case k8s.KindNetworkFold:
		return "Services"
	case k8s.KindIngressFold:
		return "Ingresses"
	case k8s.KindStorageFold:
		return "PVCs"
	case k8s.KindService:
		return "Ingresses"
	case k8s.KindPVC:
		return "PersistentVolumes"
	}
	return ""
}

func pctOf(used, total int64) string {
	if total <= 0 {
		return "—"
	}
	pct := used * 100 / total
	return fmt.Sprintf("%d%%", pct)
}

// perMille rescales used/total into 0-1000 ("per-mille") so a node's
// utilization can be pushed into a MetricBuffer whose floor is 1000 — the
// sparkline's gradient (green <50%, yellow 50-80%, red ≥80%) then aligns
// directly with the percentage text rendered next to it. Returns 0 when
// total is zero so the buffer absorbs idle samples cleanly rather than
// crashing on div-by-zero.
func perMille(used, total int64) int64 {
	if total <= 0 {
		return 0
	}
	return used * 1000 / total
}

// fracOf is the float counterpart to pctOf — returns the utilization
// fraction in [0,1] (or 0 when there's no allocation to divide against).
// Used by the row renderers to drive theme.UtilColor for the numeric cell.
func fracOf(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total)
}

// ── Body rendering (per-kind dispatch) ───────────────────────────────

// treeEmpty is the one-shape empty-state line every per-kind renderer uses
// when the list comes back zero. Keeps the wording uniform ("no X yet") so
// the operator reads the same idiom in every panel — was a mix of "no X",
// "(empty)", and "…" before. Reserved for the API-said-zero case; the
// genuinely-indeterminate "load not yet started" still renders as "…".
func (t *Tree) treeEmpty(c *tuictx.Ctx, kindLabel string) string {
	return c.Theme.Idle.Render("no " + kindLabel + " yet")
}

func (t *Tree) renderBody(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme

	if t.Loading && (t.children == nil) {
		// Spinner only shows past the gate's ShowAfter threshold —
		// fast cache hits go straight to rows.
		if t.loadGate.Visible() {
			return th.Idle.Render(c.Spinner.View() + " loading resources…")
		}
		return th.Idle.Render("…")
	}
	// Distinguish "load failed" from "no children yet" (in-flight) and from
	// "API said zero rows" (handled by the per-kind renderers below). Without
	// this branch, an unreachable cluster falls through to a silent ellipsis
	// while the operator wonders whether the tree is just slow or broken.
	if t.children == nil && t.LoadErr != nil {
		hint := th.Hint.Render("press r to retry")
		return lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(th.P.Red).Render("✗ "+t.LoadErr.Error()),
			"",
			hint,
		)
	}
	if t.children == nil {
		return th.Idle.Render("…")
	}

	if t.filter.Value() != "" && t.visibleCount() == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			th.Idle.Render("No matching resources"),
			th.Hint.Render("Change or clear the filter."))
	}

	switch t.children.Kind {
	case k8s.KindContextList:
		return t.renderContextRows(c, innerW, rowsAvail)
	case k8s.KindNamespaceFolder:
		return t.renderNamespaceFolderRows(c, innerW, rowsAvail)
	case k8s.KindInfrastructureFold:
		return t.renderInfrastructureRows(c, innerW, rowsAvail)
	case k8s.KindApp:
		return t.renderAppRows(c, innerW, rowsAvail)
	case k8s.KindPod:
		return t.renderPodRows(c, innerW, rowsAvail)
	case k8s.KindContainer:
		return t.renderContainerRows(c, innerW, rowsAvail)
	case k8s.KindNodePool:
		return t.renderPoolRows(c, innerW, rowsAvail)
	case k8s.KindNode:
		return t.renderNodeRows(c, innerW, rowsAvail)
	case k8s.KindNamespace:
		return t.renderNamespaceRows(c, innerW, rowsAvail)
	case k8s.KindConfigMap:
		return t.renderConfigMapRows(c, innerW, rowsAvail)
	case k8s.KindService:
		return t.renderServiceRows(c, innerW, rowsAvail)
	case k8s.KindIngress:
		return t.renderIngressRows(c, innerW, rowsAvail)
	case k8s.KindSecret:
		return t.renderSecretRows(c, innerW, rowsAvail)
	case k8s.KindPVC:
		return t.renderPVCRows(c, innerW, rowsAvail)
	case k8s.KindPV:
		return t.renderPVRows(c, innerW, rowsAvail)
	case k8s.KindGenericResource:
		return t.renderGenericRows(c, innerW, rowsAvail)
	}
	return th.Idle.Render("(empty)")
}

// visibleRows returns the list of refs corresponding to the current rendered
// rows, in display order — used by SelectedRef and Enter dispatch.
func (t *Tree) visibleRows() []NodeRef {
	if t.children == nil {
		return nil
	}
	switch t.children.Kind {
	case k8s.KindContextList:
		ctxs := t.filteredContexts()
		out := make([]NodeRef, len(ctxs))
		for i, ci := range ctxs {
			out[i] = NodeRef{
				Kind:    k8s.KindContext,
				Name:    ci.Name,
				Display: ci.Name,
			}
		}
		return out
	case k8s.KindNamespaceFolder:
		// Cluster-root is workload-first: All (fast path) + every user
		// namespace, then a single Infrastructure fold that holds Nodes,
		// System namespaces, and the Configure resources settings entry.
		// Before this layout, those three sat inline as peers of
		// `default/`, `payments/`, which was a category mix the user
		// flagged as confusing.
		out := make([]NodeRef, 0, 2+len(t.children.UserNS))
		if t.filter.Value() == "" {
			out = append(out, NodeRef{
				Kind:    k8s.KindAllNamespacesFold,
				Name:    "All",
				Display: fmt.Sprintf("All (%d)", len(t.children.Apps)),
				Health:  k8s.HealthUnknown,
			})
		}
		nss := t.filteredUserNamespaces()
		for _, ns := range nss {
			out = append(out, NodeRef{
				Kind: k8s.KindNamespaceFolder, Name: ns.Name, Namespace: ns.Name,
				Display: ns.Name,
			})
		}
		if t.filter.Value() == "" {
			out = append(out, NodeRef{
				Kind:    k8s.KindInfrastructureFold,
				Name:    "Infrastructure",
				Display: "Infrastructure",
				Health:  k8s.HealthUnknown,
			})
		}
		return out
	case k8s.KindInfrastructureFold:
		// Children of the Infrastructure fold: Nodes + System
		// namespaces. Settings used to live here too; it now opens via
		// the `,` keybinding from anywhere (more discoverable in the
		// footer; doesn't pollute the navigation tree).
		out := make([]NodeRef, 0, 2)
		out = append(out, NodeRef{
			Kind:    k8s.KindClusterFold,
			Name:    "Nodes",
			Display: t.foldDisplay("Nodes", "ClusterFold", ""),
			Health:  k8s.HealthUnknown,
		})
		if t.children != nil && t.children.SystemCount > 0 {
			out = append(out, NodeRef{
				Kind:    k8s.KindSystemFold,
				Name:    "System",
				Display: fmt.Sprintf("System (%d)", t.children.SystemCount),
				Health:  k8s.HealthUnknown,
			})
		}
		return out
	case k8s.KindApp:
		apps := t.filteredApps()
		// "In a folder" means the focus is one of: the virtual All folder,
		// a user-namespace folder, or a system namespace row. Those are
		// the levels where the renderer mixes Workloads/Config/Secrets/
		// Network/Storage fold rows above the apps list (or replaces apps
		// with fold rows when no filter is active — preserving the old
		// cluster-root table-of-contents feel one level down).
		focusKind := t.Focus().Kind
		inFolder := focusKind == k8s.KindAllNamespacesFold ||
			focusKind == k8s.KindNamespaceFolder ||
			focusKind == k8s.KindNamespace
		out := make([]NodeRef, 0, len(apps)+len(t.enabledResources)+1)

		if inFolder && t.filter.Value() == "" {
			// Folds are driven by AppConfig.EnabledResources — the user can
			// add CRDs / hide defaults via the Resource Browser. Labels
			// stay the plural k8s resource names so the breadcrumb segment
			// matches what `kubectl get <thing>` would return.
			specs := t.foldSpecsFor(t.enabledResources)
			for _, spec := range specs {
				display := t.foldDisplay(spec.Label, spec.CountKind, spec.GVR)
				if spec.Kind == k8s.KindWorkloadsFold {
					// Workloads count is already on hand (we always load
					// apps for the namespace) — skip the cache lookup so
					// the badge populates without a drill.
					display = fmt.Sprintf("%s (%d)", spec.Label, len(t.children.Apps))
				}
				out = append(out, NodeRef{
					Kind:    spec.Kind,
					Name:    spec.Label,
					Display: display,
					GVR:     spec.GVR,
					Health:  k8s.HealthUnknown,
				})
			}
			// If the user hid every fold via the Resource Browser, fall
			// through to render the apps directly — otherwise the namespace
			// would look empty even though deployments exist, with no
			// on-screen way back to the Browser other than the `,` chord.
			if len(specs) > 0 {
				return out
			}
		}

		// One level deeper (under Workloads, or via filter-typed search):
		// the children ARE the apps. Emit them directly.
		for _, a := range apps {
			out = append(out, NodeRef{
				Kind: k8s.KindApp, SubKind: a.Kind,
				Namespace: a.Namespace, Name: a.Name, UID: a.UID,
				Display: a.Name, Health: a.Health,
			})
		}
		return out
	case k8s.KindPod:
		pods := t.filteredPods()
		out := make([]NodeRef, len(pods))
		for i, p := range pods {
			out[i] = NodeRef{
				Kind: k8s.KindPod, Namespace: p.Namespace, Name: p.Name,
				Display: p.Name, Health: k8s.PodHealth(p),
			}
		}
		return out
	case k8s.KindContainer:
		ctrs := t.filteredContainers()
		out := make([]NodeRef, len(ctrs))
		for i, ci := range ctrs {
			out[i] = NodeRef{
				Kind: k8s.KindContainer, Namespace: ci.Namespace, Name: ci.Name,
				Display: ci.Name, Health: k8s.ContainerHealth(ci),
			}
		}
		return out
	case k8s.KindNodePool:
		pools := t.filteredPools()
		out := make([]NodeRef, len(pools))
		for i, p := range pools {
			out[i] = NodeRef{Kind: k8s.KindNodePool, Name: p.Name, Display: p.Name, Health: p.Health}
		}
		return out
	case k8s.KindNode:
		nodes := t.filteredNodes()
		out := make([]NodeRef, len(nodes))
		for i, n := range nodes {
			out[i] = NodeRef{Kind: k8s.KindNode, Name: n.Name, UID: n.UID, Display: n.Name, Health: n.Health}
		}
		return out
	case k8s.KindNamespace:
		nss := t.filteredNamespaces()
		out := make([]NodeRef, len(nss))
		for i, n := range nss {
			out[i] = NodeRef{Kind: k8s.KindNamespace, Name: n.Name, Display: n.Name}
		}
		return out
	case k8s.KindConfigMap:
		cms := t.filteredConfigMaps()
		out := make([]NodeRef, len(cms))
		for i, cm := range cms {
			out[i] = NodeRef{Kind: k8s.KindConfigMap, Namespace: cm.Namespace, Name: cm.Name, Display: cm.Name}
		}
		return out
	case k8s.KindService:
		svcs := t.filteredServices()
		out := make([]NodeRef, len(svcs))
		for i, s := range svcs {
			out[i] = NodeRef{Kind: k8s.KindService, Namespace: s.Namespace, Name: s.Name, Display: s.Name}
		}
		return out
	case k8s.KindIngress:
		ings := t.filteredIngresses()
		out := make([]NodeRef, len(ings))
		for i, ing := range ings {
			out[i] = NodeRef{Kind: k8s.KindIngress, Namespace: ing.Namespace, Name: ing.Name, Display: ing.Name}
		}
		return out
	case k8s.KindSecret:
		secrets := t.filteredSecrets()
		out := make([]NodeRef, len(secrets))
		for i, s := range secrets {
			out[i] = NodeRef{Kind: k8s.KindSecret, Namespace: s.Namespace, Name: s.Name, Display: s.Name}
		}
		return out
	case k8s.KindPVC:
		pvcs := t.filteredPVCs()
		out := make([]NodeRef, len(pvcs))
		for i, p := range pvcs {
			out[i] = NodeRef{Kind: k8s.KindPVC, Namespace: p.Namespace, Name: p.Name, Display: p.Name}
		}
		return out
	case k8s.KindPV:
		pvs := t.filteredPVs()
		out := make([]NodeRef, len(pvs))
		for i, p := range pvs {
			out[i] = NodeRef{Kind: k8s.KindPV, Name: p.Name, Display: p.Name}
		}
		return out
	case k8s.KindGenericResource:
		items := t.filteredGeneric()
		// Generic rows inherit the GVR from the parent CustomFold so verb
		// dispatch (describe/edit/delete) can route via the dynamic client
		// without re-resolving the kind from the breadcrumb.
		gvr := ""
		for i := len(t.path) - 1; i >= 0; i-- {
			if t.path[i].Kind == k8s.KindCustomFold {
				gvr = t.path[i].GVR
				break
			}
		}
		out := make([]NodeRef, len(items))
		for i, r := range items {
			out[i] = NodeRef{
				Kind:      k8s.KindGenericResource,
				Namespace: r.Namespace, Name: r.Name, UID: r.UID,
				Display: r.Name, GVR: gvr,
			}
		}
		return out
	}
	return nil
}

func (t *Tree) visibleCount() int { return len(t.visibleRows()) }

// ── Per-kind row rendering ───────────────────────────────────────────

func (t *Tree) renderAppRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	apps := t.filteredApps()
	rows := t.visibleRows()
	if len(rows) == 0 {
		return t.treeEmpty(c, "apps")
	}

	// Progressive disclosure of columns: NS only when there's more than one
	// distinct user namespace on screen (no point showing "default" 50×);
	// HPA only when at least one app is autoscaled (drops the column when
	// every row would be empty, eliminating the gap between health and the
	// metric sparks); CPU/MEM only when at least one row has a real value
	// (drops the column entirely on clusters where metrics-server is absent).
	// Reclaimed cells flow into NAME so long deployment names stop truncating.
	showNS := distinctAppNamespaces(apps) > 1
	showHPA := anyAppHPA(apps)
	showMetrics := innerW >= 50 && anyAppMetrics(apps)

	tableW := innerW - components.EdgeTotal()
	const (
		kW = 1
		hW = 5
		// cpuW / memW hold only the sparkline now — the numeric tail moved
		// to the Inspector tile, where the right pane has room for the
		// precise current value plus configured requests/limits. The
		// per-row spark stays narrow so the list scans fast.
		cpuW    = sparkWidth
		memW    = sparkWidth
		agW     = 5
		nameMin = 10
	)
	nsW := 10
	// HPA narrowed to match Pods' RST slot (4 cells). The previous
	// "↕5 (2–10)" form ate 11 cells and pushed AGE out of alignment
	// with the Pod table below. The current value alone ("↕5") is
	// the at-a-glance signal — min/max live in the Inspector tile,
	// one keystroke away.
	hpaW := 4
	if !showNS {
		nsW = 0
	}
	if !showHPA {
		hpaW = 0
	}
	colsCount := 4 // K, NAME, H, AGE — always
	if showNS {
		colsCount++
	}
	if showHPA {
		colsCount++
	}
	if showMetrics {
		colsCount += 2
	}
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		usedCpuW = cpuW
		usedMemW = memW
	}
	for {
		gaps := components.GapTotal(colsCount)
		nW := tableW - kW - nsW - hW - hpaW - usedCpuW - usedMemW - agW - gaps
		if nW >= nameMin {
			break
		}
		if showNS && nsW > 4 {
			nsW--
			continue
		}
		if showHPA && hpaW > 4 {
			hpaW--
			continue
		}
		break
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - nsW - hW - hpaW - usedCpuW - usedMemW - agW - gaps
	if nW < nameMin {
		nW = nameMin
	}

	// Column order: K · NAME · NS? · H · CPU? · MEM? · HPA? · AGE.
	// HPA sits in the same trailing slot Pods use for RST so the
	// shared columns (K / NAME / NS / H / CPU / MEM / AGE) line up
	// vertically when the user drills from Workloads to Pods — the
	// kind-specific column (HPA on apps, RST on pods) is the only
	// thing that shifts in/out.
	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME"}
	if showNS {
		cols = append(cols, nsW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "NS")
	}
	cols = append(cols, hW)
	aligns = append(aligns, components.AlignLeft)
	headerLabels = append(headerLabels, "READY")
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	if showHPA {
		cols = append(cols, hpaW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "HPA")
	}
	cols = append(cols, agW)
	aligns = append(aligns, components.AlignRight)
	headerLabels = append(headerLabels, "AGE")
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(rows), rowsAvail-1)
	out := []string{header}
	// Compute how many rows precede the apps slice (a Cluster sentinel
	// occupies index 0 when present), so we can map row index → app index.
	appOffset := 0
	for appOffset < len(rows) && rows[appOffset].Kind != k8s.KindApp {
		appOffset++
	}
	for i := start; i < end; i++ {
		ref := rows[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		if ref.Kind == k8s.KindClusterFold || ref.Kind == k8s.KindSystemFold ||
			ref.Kind == k8s.KindConfigFold || ref.Kind == k8s.KindNetworkFold ||
			ref.Kind == k8s.KindIngressFold || ref.Kind == k8s.KindSecretsFold ||
			ref.Kind == k8s.KindStorageFold || ref.Kind == k8s.KindWorkloadsFold ||
			ref.Kind == k8s.KindAllNamespacesFold || ref.Kind == k8s.KindNamespaceFolder ||
			ref.Kind == k8s.KindCustomFold {
			vals := []string{
				kindGlyph(ref.Kind, ""),
				th.NameStrongOn(components.TruncPad(ref.Display, nW), bg),
			}
			for j := 2; j < len(cols); j++ {
				vals = append(vals, "")
			}
			out = append(out, components.Row(th, vals, cols, aligns, selected, i))
			continue
		}
		var a k8s.AppInfo
		ai := i - appOffset
		if ai >= 0 && ai < len(apps) {
			a = apps[ai]
		}
		health := th.MetaOn(components.TruncPad(a.Ready, hW), bg)
		if a.Health == k8s.HealthFailing {
			health = bg.Foreground(th.P.Red).Bold(true).Render(components.TruncPad(a.Ready, hW))
		}
		if a.Health == k8s.HealthDegraded {
			health = bg.Foreground(th.P.Yellow).Render(components.TruncPad(a.Ready, hW))
		}
		if t.isPulsing(k8s.KindApp, a.Namespace, a.Name) {
			health = bg.Foreground(th.P.FG).Reverse(true).Render(components.TruncPad(a.Ready, hW))
		}
		if a.Ready == "" {
			health = healthGlyphStyled(th, a.Health, bg, t.isPulsing(k8s.KindApp, a.Namespace, a.Name))
		}
		var hpaCell string
		if a.HPA != nil {
			hpaText := components.TruncPad(fmt.Sprintf("↕%d", a.HPA.Current), hpaW)
			if a.HPA.PinnedAtMax {
				hpaCell = bg.Foreground(th.P.Red).Bold(true).Render(hpaText)
			} else {
				hpaCell = th.MetaOn(hpaText, bg)
			}
		}
		nameCell := th.NameStrongOn(components.TruncPad(a.Name, nW), bg)
		ageCell := th.Age(a.Age, bg)

		vals := []string{appKindGlyph(a.Kind), nameCell}
		if showNS {
			vals = append(vals, th.MetaOn(components.TruncPad(a.Namespace, nsW), bg))
		}
		vals = append(vals, health)
		if showMetrics {
			cpuBuf, memBuf := t.metricSeriesFor(k8s.KindApp, a.Namespace, a.Name)
			vals = append(vals,
				renderMetricCell(th, cpuBuf, bg, c.Stale),
				renderMetricCell(th, memBuf, bg, c.Stale),
			)
		}
		if showHPA {
			vals = append(vals, hpaCell)
		}
		vals = append(vals, ageCell)

		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// renderMetricCell renders the per-row CPU/MEM sparkline. The absolute
// value used to sit next to the spark, but the left pane is the φ-small
// side and a 4-cell spark already encodes the signal a list-scan needs
// ("which app is hot?"). The Inspector tile carries the precise current
// value (and requests/limits) for whichever row the cursor's on — that's
// where you go for "how hot, exactly." The row's bg style threads through
// so the spark inherits stripe/select painting rather than punching a
// terminal-default gap into the column.
//
// `stale` flips the renderer to a dim, color-suppressed variant when the
// data on screen is older than the auto-refresh cadence (or the terminal
// lost focus and refreshes are paused). The trend shape stays visible;
// the urgency colors don't until the next fresh sample arrives.
func renderMetricCell(th *theme.Theme, buf *components.MetricBuffer, bg lipgloss.Style, stale bool) string {
	if stale {
		return components.SparklineDim(th, buf, sparkWidth, bg)
	}
	return components.Sparkline(th, buf, sparkWidth, bg)
}

// renderUtilMetricCell is the percentage-shaped sibling of renderMetricCell:
// the numeric value is colorized with the same green/yellow/red gradient
// the sparkline uses, so the whole cell reads as a single utilization band
// instead of "red bars next to a dim 92%". `frac` is the utilization in
// [0,1]; values outside the range still color sensibly (clamped via
// theme.UtilColor's gradient). When `stale`, both the spark and the value
// drop to a dim register so the whole cell reads as "old data" rather than
// "this is live and 92%".
func renderUtilMetricCell(th *theme.Theme, buf *components.MetricBuffer, value string, frac float64, bg lipgloss.Style, stale bool) string {
	var spark string
	var valStyle lipgloss.Style
	if stale {
		spark = components.SparklineDim(th, buf, sparkWidth, bg)
		valStyle = bg.Foreground(th.P.Dim)
	} else {
		spark = components.Sparkline(th, buf, sparkWidth, bg)
		valStyle = bg.Foreground(th.UtilColor(frac)).Bold(true)
	}
	val := valStyle.Render(components.TruncPad(value, 4))
	gap := bg.Render(" ")
	return spark + gap + val
}

// distinctAppNamespaces counts the unique namespaces in the visible app list.
// Used to decide whether the NS column carries any signal.
func distinctAppNamespaces(apps []k8s.AppInfo) int {
	if len(apps) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(apps))
	for _, a := range apps {
		seen[a.Namespace] = struct{}{}
	}
	return len(seen)
}

// anyAppMetrics reports whether at least one app has live metrics. When
// false (metrics-server unavailable / not yet propagated) the CPU/MEM
// columns drop entirely.
func anyAppMetrics(apps []k8s.AppInfo) bool {
	for _, a := range apps {
		if a.CPU > 0 || a.Memory > 0 {
			return true
		}
	}
	return false
}

// anyAppHPA reports whether at least one app is autoscaled. The HPA column
// is hidden when the entire visible set is statically-replica'd — otherwise
// the 11-cell column sits empty between the health glyph and the metric
// sparklines, reading as a misalignment rather than reserved space.
func anyAppHPA(apps []k8s.AppInfo) bool {
	for _, a := range apps {
		if a.HPA != nil {
			return true
		}
	}
	return false
}

func anyIngressHosts(ings []k8s.IngressInfo) bool {
	for _, ing := range ings {
		if ing.Hosts != "" {
			return true
		}
	}
	return false
}

func anyPVCVolume(pvcs []k8s.PVCInfo) bool {
	for _, p := range pvcs {
		if p.VolumeName != "" {
			return true
		}
	}
	return false
}

func anyPVClaim(pvs []k8s.PVInfo) bool {
	for _, p := range pvs {
		if p.Claim != "" {
			return true
		}
	}
	return false
}

func anyPVAccessModes(pvs []k8s.PVInfo) bool {
	for _, p := range pvs {
		if p.AccessModes != "" {
			return true
		}
	}
	return false
}

func anyPVReclaim(pvs []k8s.PVInfo) bool {
	for _, p := range pvs {
		if p.Reclaim != "" {
			return true
		}
	}
	return false
}

// renderPodRows mirrors renderAppRows' column shape so the eye doesn't
// have to relearn the table on every drill — same K · NAME · NS? · H ·
// CPU? · MEM? · RST? · AGE layout, with progressive disclosure for NS,
// metrics, and restarts. The 18-cell STATUS text column was retired in
// favour of the 1-cell H glyph (Running collapses to a quiet green dot;
// abnormal states paint a colored glyph instead — operators drill into
// the Inspector for the full status label like "ImagePullBackOff").
func (t *Tree) renderPodRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pods := t.filteredPods()
	if len(pods) == 0 {
		return t.treeEmpty(c, "pods")
	}
	showNS := distinctPodNamespaces(pods) > 1
	showMetrics := innerW >= 50 && anyPodMetrics(pods)
	showRST := anyPodRestarts(pods)

	tableW := innerW - components.EdgeTotal()
	const (
		kW = 1
		hW = 1
		// Spark-only — see renderMetricCell. The precise current value
		// (and configured req/limit) lives in the Inspector tile.
		cpuW    = sparkWidth
		memW    = sparkWidth
		rsW     = 4
		agW     = 5
		nameMin = 10
	)
	nsW := 10
	if !showNS {
		nsW = 0
	}
	colsCount := 4 // K, NAME, H, AGE — always
	if showNS {
		colsCount++
	}
	if showMetrics {
		colsCount += 2
	}
	if showRST {
		colsCount++
	}
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		usedCpuW = cpuW
		usedMemW = memW
	}
	usedRsW := 0
	if showRST {
		usedRsW = rsW
	}
	for {
		gaps := components.GapTotal(colsCount)
		nW := tableW - kW - nsW - hW - usedCpuW - usedMemW - usedRsW - agW - gaps
		if nW >= nameMin {
			break
		}
		if showNS && nsW > 4 {
			nsW--
			continue
		}
		break
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - nsW - hW - usedCpuW - usedMemW - usedRsW - agW - gaps
	if nW < nameMin {
		nW = nameMin
	}

	// Column order: K · NAME · NS? · H · CPU? · MEM? · RST? · AGE.
	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME"}
	if showNS {
		cols = append(cols, nsW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "NS")
	}
	cols = append(cols, hW)
	aligns = append(aligns, components.AlignLeft)
	headerLabels = append(headerLabels, "")
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	if showRST {
		cols = append(cols, usedRsW)
		aligns = append(aligns, components.AlignRight)
		headerLabels = append(headerLabels, "RST")
	}
	cols = append(cols, agW)
	aligns = append(aligns, components.AlignRight)
	headerLabels = append(headerLabels, "AGE")
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pods), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pods[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncMiddlePad(p.Name, nW), bg)
		// StatusGlyph at maxW=1 collapses to just the colored dot (the
		// abnormal-state word is dropped). Same shape Apps use for H.
		statusCell := th.StatusGlyph(p.Status, 1, bg)
		rstCell := th.RestartCount(int(p.Restarts), bg)
		ageCell := th.Age(p.Age, bg)

		vals := []string{kindGlyph(k8s.KindPod, ""), nameCell}
		if showNS {
			vals = append(vals, th.MetaOn(components.TruncPad(p.Namespace, nsW), bg))
		}
		vals = append(vals, statusCell)
		if showMetrics {
			cpuBuf, memBuf := t.metricSeriesFor(k8s.KindPod, p.Namespace, p.Name)
			vals = append(vals,
				renderMetricCell(th, cpuBuf, bg, c.Stale),
				renderMetricCell(th, memBuf, bg, c.Stale),
			)
		}
		if showRST {
			vals = append(vals, rstCell)
		}
		vals = append(vals, ageCell)
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func anyPodMetrics(pods []k8s.PodInfo) bool {
	for _, p := range pods {
		if p.CPU > 0 || p.Memory > 0 {
			return true
		}
	}
	return false
}

// distinctPodNamespaces counts the unique namespaces in the visible pod
// list. The NS column kicks in only when pods span more than one — a
// single-namespace drill (e.g. all pods of one deployment) suppresses
// the column so NAME inherits the cells.
func distinctPodNamespaces(pods []k8s.PodInfo) int {
	if len(pods) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(pods))
	for _, p := range pods {
		seen[p.Namespace] = struct{}{}
	}
	return len(seen)
}

// anyPodRestarts reports whether at least one pod has restarted. RST is
// the textbook "watch for outliers" column — when every pod is at 0,
// suppressing the column reclaims width and matches the affordance
// (absence is the affirmation) the H column already uses.
func anyPodRestarts(pods []k8s.PodInfo) bool {
	for _, p := range pods {
		if p.Restarts > 0 {
			return true
		}
	}
	return false
}

// renderContainerRows mirrors renderPodRows' visual register so a drill
// from a pod into its containers reads as the same table, not a
// different one — same K · NAME · H · CPU? · MEM? · RST? column types
// and widths. STATE-as-text (14 cells) and CPU/MEM-as-numbers were the
// previous shape; they made every state transition (pod → containers)
// feel like a different view. Containers don't carry an AGE separate
// from the parent pod, and they all share its namespace, so NS and
// AGE columns are absent — every other column lines up with the pod
// row above it on screen during a deep drill.
func (t *Tree) renderContainerRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	cs := t.filteredContainers()
	if len(cs) == 0 {
		return t.treeEmpty(c, "containers")
	}
	showMetrics := anyContainerMetrics(cs)
	showRST := anyContainerRestarts(cs)

	tableW := innerW - components.EdgeTotal()
	const (
		kW = 1
		hW = 1
		// Same shape as renderPodRows: a single-cell H glyph (colored
		// dot encoding the State), then 4-cell sparklines for CPU / MEM.
		// Precise current values + image ref live in the Inspector tile.
		cpuW    = sparkWidth
		memW    = sparkWidth
		rsW     = 4
		nameMin = 10
	)
	colsCount := 3 // K, NAME, H — always
	usedCpuW, usedMemW := 0, 0
	if showMetrics {
		colsCount += 2
		usedCpuW, usedMemW = cpuW, memW
	}
	usedRsW := 0
	if showRST {
		colsCount++
		usedRsW = rsW
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - hW - usedCpuW - usedMemW - usedRsW - gaps
	if nW < nameMin {
		nW = nameMin
	}

	// Column order: K · NAME · H · CPU? · MEM? · RST?
	cols := []int{kW, nW, hW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME · IMAGE", ""}
	if showMetrics {
		cols = append(cols, usedCpuW, usedMemW)
		aligns = append(aligns, components.AlignRight, components.AlignRight)
		headerLabels = append(headerLabels, "CPU", "MEM")
	}
	if showRST {
		cols = append(cols, usedRsW)
		aligns = append(aligns, components.AlignRight)
		headerLabels = append(headerLabels, "RST")
	}
	header := components.Header(th, headerLabels, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(cs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ci := cs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameW := nW / 2
		imgW := nW - nameW
		// init/ephemeral containers keep their inline kind prefix on the name
		// cell ([init], [eph]) — the K column carries the consistent
		// "container" mark, and the inline tag carries the sub-kind nuance.
		namePrefix := ""
		if ci.Kind != "regular" {
			namePrefix = th.MetaOn("["+shortKind(ci.Kind)+"] ", bg)
		}
		nameCell := namePrefix +
			th.NameStrongOn(components.TruncPad(ci.Name, nameW-lipgloss.Width(namePrefix)), bg) +
			th.MetaOn(components.TruncPad(" · "+ci.Image, imgW), bg)
		// StatusGlyph(maxW=1) collapses to just the colored dot — same
		// shape Apps and Pods use for H. The Inspector tile carries the
		// full "Waiting: ImagePullBackOff" label for the selected row.
		healthCell := th.StatusGlyph(ci.State, 1, bg)

		vals := []string{kindGlyph(k8s.KindContainer, ""), nameCell, healthCell}
		if showMetrics {
			cpuBuf, memBuf := t.metricSeriesFor(k8s.KindContainer, ci.Namespace, ci.PodName+"/"+ci.Name)
			vals = append(vals,
				renderMetricCell(th, cpuBuf, bg, c.Stale),
				renderMetricCell(th, memBuf, bg, c.Stale),
			)
		}
		if showRST {
			vals = append(vals, th.RestartCount(int(ci.Restarts), bg))
		}
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// anyContainerRestarts mirrors anyPodRestarts: gates the RST column so
// it disappears when no container in view has restarted, reclaiming the
// width for NAME · IMAGE.
func anyContainerRestarts(cs []k8s.ContainerInfo) bool {
	for _, c := range cs {
		if c.Restarts > 0 {
			return true
		}
	}
	return false
}

func anyContainerMetrics(cs []k8s.ContainerInfo) bool {
	for _, c := range cs {
		if c.CPU > 0 || c.Memory > 0 {
			return true
		}
	}
	return false
}

func (t *Tree) renderPoolRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pools := t.filteredPools()
	if len(pools) == 0 {
		return t.treeEmpty(c, "node pools")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW    = 1
		hW    = 1
		nodes = 6
		cpuW  = sparkWidth + 1 + 4 // sparkline + space + 4-char "100%"
		memW  = sparkWidth + 1 + 4
		podsW = 9
	)
	gaps := components.GapTotal(7)
	nW := tableW - kW - hW - nodes - cpuW - memW - podsW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, hW, nW, nodes, cpuW, memW, podsW}
	aligns := []components.Align{
		components.AlignLeft, components.AlignLeft, components.AlignLeft,
		components.AlignRight, components.AlignRight, components.AlignRight, components.AlignRight,
	}
	header := components.Header(th, []string{"", "", "NAME", "NODES", "CPU%", "MEM%", "PODS"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pools), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pools[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		health := healthGlyphColored(th, p.Health, bg)
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nW), bg)
		nodesCell := strconv.Itoa(p.Nodes)
		cpuBuf, memBuf := t.metricSeriesFor(k8s.KindNodePool, "", p.Name)
		cpuCell := renderUtilMetricCell(th, cpuBuf, pctOf(p.CPUUsed, p.CPUAlloc), fracOf(p.CPUUsed, p.CPUAlloc), bg, c.Stale)
		memCell := renderUtilMetricCell(th, memBuf, pctOf(p.MemUsed, p.MemAlloc), fracOf(p.MemUsed, p.MemAlloc), bg, c.Stale)
		pods := fmt.Sprintf("%d/%d", p.PodsCurrent, p.PodsMax)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNodePool, ""), health, nameCell, nodesCell, cpuCell, memCell, pods},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderNodeRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	nodes := t.filteredNodes()
	if len(nodes) == 0 {
		return t.treeEmpty(c, "nodes")
	}
	tableW := innerW - components.EdgeTotal()
	// prW is a small fixed-width column for the node's active pressure
	// conditions (MemoryPressure/DiskPressure/PIDPressure/NetworkUnavailable).
	// Most rows render empty here; nodes in trouble light up — the same role
	// btop's temperature column plays for hardware throttling.
	const (
		kW   = 1
		hW   = 1
		stW  = 12
		prW  = 4
		cpuW = sparkWidth + 1 + 4 // sparkline + space + 4-char "100%"
		memW = sparkWidth + 1 + 4
		podW = 9
	)
	gaps := components.GapTotal(8)
	nW := tableW - kW - hW - stW - prW - cpuW - memW - podW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, hW, nW, stW, prW, cpuW, memW, podW}
	aligns := []components.Align{
		components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft,
		components.AlignLeft, components.AlignRight, components.AlignRight, components.AlignRight,
	}
	header := components.Header(th, []string{"", "", "NAME", "STATUS", "PRES", "CPU%", "MEM%", "PODS"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(nodes), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		n := nodes[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		health := healthGlyphColored(th, n.Health, bg)
		nameCell := th.NameStrongOn(components.TruncPad(n.Name, nW), bg)
		statusCell := th.StatusGlyph(n.Status, stW, bg)
		pressureCell := renderPressureCell(th, n.Pressures, prW, bg)
		cpuBuf, memBuf := t.metricSeriesFor(k8s.KindNode, "", n.Name)
		cpuCell := renderUtilMetricCell(th, cpuBuf, pctOf(n.CPUUsed, n.CPUAlloc), fracOf(n.CPUUsed, n.CPUAlloc), bg, c.Stale)
		memCell := renderUtilMetricCell(th, memBuf, pctOf(n.MemUsed, n.MemAlloc), fracOf(n.MemUsed, n.MemAlloc), bg, c.Stale)
		pods := fmt.Sprintf("%d/%d", n.PodsCurrent, n.PodsMax)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNode, ""), health, nameCell, statusCell, pressureCell, cpuCell, memCell, pods},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// renderPressureCell stamps the active pressure-condition glyphs for a
// node into a fixed-width cell. Mp = MemoryPressure, Dp = DiskPressure,
// Pp = PIDPressure, Nu = NetworkUnavailable — short two-letter labels
// because the on-caller's eye is scanning a column, not reading prose,
// and the colour (red bold) is the load-bearing signal. Empty when the
// node has no active pressure conditions.
func renderPressureCell(th *theme.Theme, pressures []string, width int, bg lipgloss.Style) string {
	if len(pressures) == 0 {
		return bg.Width(width).Render("")
	}
	var b strings.Builder
	for i, p := range pressures {
		if i > 0 {
			b.WriteString(bg.Render(" "))
		}
		var label string
		switch p {
		case "MemoryPressure":
			label = "Mp"
		case "DiskPressure":
			label = "Dp"
		case "PIDPressure":
			label = "Pp"
		case "NetworkUnavailable":
			label = "Nu"
		default:
			if len(p) >= 2 {
				label = p[:2]
			} else {
				label = p
			}
		}
		b.WriteString(bg.Foreground(th.P.Red).Bold(true).Render(label))
	}
	out := b.String()
	if lipgloss.Width(out) > width {
		return out
	}
	return out + bg.Width(width-lipgloss.Width(out)).Render("")
}

func (t *Tree) renderConfigMapRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	cms := t.filteredConfigMaps()
	if len(cms) == 0 {
		return t.treeEmpty(c, "configmaps")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		agW  = 5
		keyW = 4
	)
	gaps := components.GapTotal(4)
	nW := tableW - kW - keyW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW, keyW, agW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignRight}
	header := components.Header(th, []string{"", "NAME", "KEYS", "AGE"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(cms), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		cm := cms[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(cm.Name, nW), bg)
		keysCell := th.MetaOn(strconv.Itoa(cm.Keys), bg)
		ageCell := th.Age(cm.Age, bg)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindConfigMap, ""), nameCell, keysCell, ageCell},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderServiceRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	svcs := t.filteredServices()
	if len(svcs) == 0 {
		return t.treeEmpty(c, "services")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW    = 1
		agW   = 5
		typW  = 12
		portW = 14
		ipW   = 15
	)
	// PORTS lives between TYPE and CLUSTER-IP. Drop it first under narrow
	// widths so the address column stays readable.
	showPorts := tableW-kW-typW-portW-ipW-agW-components.GapTotal(6) >= 10
	colsCount := 5
	if showPorts {
		colsCount = 6
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - typW - ipW - agW - gaps
	if showPorts {
		nW -= portW
	}
	if nW < 10 {
		nW = 10
	}
	var (
		cols    []int
		aligns  []components.Align
		headers []string
	)
	if showPorts {
		cols = []int{kW, nW, typW, portW, ipW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "TYPE", "PORTS", "CLUSTER-IP", "AGE"}
	} else {
		cols = []int{kW, nW, typW, ipW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "TYPE", "CLUSTER-IP", "AGE"}
	}
	header := components.Header(th, headers, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(svcs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		s := svcs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(s.Name, nW), bg)
		typeCell := th.MetaOn(components.TruncPad(s.Type, typW), bg)
		ipCell := th.MetaOn(components.TruncPad(s.ClusterIP, ipW), bg)
		ageCell := th.Age(s.Age, bg)
		cells := []string{kindGlyph(k8s.KindService, ""), nameCell, typeCell}
		if showPorts {
			portText := s.Ports
			if portText == "" {
				portText = "—"
			}
			portsCell := th.MetaOn(components.TruncPad(portText, portW), bg)
			cells = append(cells, portsCell)
		}
		cells = append(cells, ipCell, ageCell)
		out = append(out, components.Row(th, cells, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderIngressRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	ings := t.filteredIngresses()
	if len(ings) == 0 {
		return t.treeEmpty(c, "ingresses")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW     = 1
		agW    = 5
		classW = 8
	)
	// HOSTS column takes ~30% of the remaining horizontal budget and is
	// hidden when no ingress has any host configured (gate matches the
	// progressive-disclosure idiom used for HPA / metric columns).
	showHosts := anyIngressHosts(ings)
	hostW := 0
	colsCount := 4
	if showHosts {
		colsCount = 5
		budget := tableW - kW - classW - agW - components.GapTotal(colsCount)
		hostW = budget * 3 / 10
		if hostW < 10 {
			hostW = 10
		}
		// If the name column would collapse below 10, drop the column entirely.
		if budget-hostW < 10 {
			showHosts = false
			hostW = 0
			colsCount = 4
		}
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - classW - agW - gaps - hostW
	if nW < 10 {
		nW = 10
	}
	var (
		cols    []int
		aligns  []components.Align
		headers []string
	)
	if showHosts {
		cols = []int{kW, nW, hostW, classW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "HOSTS", "CLASS", "AGE"}
	} else {
		cols = []int{kW, nW, classW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "CLASS", "AGE"}
	}
	header := components.Header(th, headers, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(ings), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ing := ings[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(ing.Name, nW), bg)
		classCell := th.MetaOn(components.TruncPad(ing.Class, classW), bg)
		ageCell := th.Age(ing.Age, bg)
		cells := []string{kindGlyph(k8s.KindIngress, ""), nameCell}
		if showHosts {
			hostsCell := th.MetaOn(components.TruncPad(ing.Hosts, hostW), bg)
			cells = append(cells, hostsCell)
		}
		cells = append(cells, classCell, ageCell)
		out = append(out, components.Row(th, cells, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderSecretRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	secrets := t.filteredSecrets()
	if len(secrets) == 0 {
		return t.treeEmpty(c, "secrets")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		agW  = 5
		typW = 16
		keyW = 4
	)
	gaps := components.GapTotal(5)
	nW := tableW - kW - typW - keyW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW, typW, keyW, agW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignRight}
	header := components.Header(th, []string{"", "NAME", "TYPE", "KEYS", "AGE"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(secrets), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		s := secrets[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(s.Name, nW), bg)
		typeCell := th.MetaOn(components.TruncPad(shortSecretType(s.Type), typW), bg)
		keysCell := th.MetaOn(strconv.Itoa(s.Keys), bg)
		ageCell := th.Age(s.Age, bg)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindSecret, ""), nameCell, typeCell, keysCell, ageCell},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderPVCRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pvcs := t.filteredPVCs()
	if len(pvcs) == 0 {
		return t.treeEmpty(c, "PVCs")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		agW  = 5
		stW  = 8
		capW = 7
		scW  = 10
		volW = 22
	)
	// VOLUME column: hidden when every PVC is unbound (e.g. Pending
	// fleet) so the column doesn't sit empty between CLASS and AGE.
	showVolume := anyPVCVolume(pvcs)
	colsCount := 6
	if showVolume {
		colsCount = 7
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - stW - capW - scW - agW - gaps
	if showVolume {
		nW -= volW
	}
	if nW < 10 {
		// Drop VOLUME first if we can't keep the name column readable.
		if showVolume {
			showVolume = false
			colsCount = 6
			gaps = components.GapTotal(colsCount)
			nW = tableW - kW - stW - capW - scW - agW - gaps
		}
		if nW < 10 {
			nW = 10
		}
	}
	var (
		cols    []int
		aligns  []components.Align
		headers []string
	)
	if showVolume {
		cols = []int{kW, nW, stW, capW, scW, volW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignLeft, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "STATUS", "CAPACITY", "CLASS", "VOLUME", "AGE"}
	} else {
		cols = []int{kW, nW, stW, capW, scW, agW}
		aligns = []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignLeft, components.AlignRight}
		headers = []string{"", "NAME", "STATUS", "CAPACITY", "CLASS", "AGE"}
	}
	header := components.Header(th, headers, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pvcs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pvcs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nW), bg)
		statusCell := th.StatusGlyph(p.Status, stW, bg)
		capCell := th.MetaOn(components.TruncPad(p.Capacity, capW), bg)
		scCell := th.MetaOn(components.TruncPad(p.StorageClass, scW), bg)
		ageCell := th.Age(p.Age, bg)
		cells := []string{kindGlyph(k8s.KindPVC, ""), nameCell, statusCell, capCell, scCell}
		if showVolume {
			volCell := th.MetaOn(components.TruncPad(p.VolumeName, volW), bg)
			cells = append(cells, volCell)
		}
		cells = append(cells, ageCell)
		out = append(out, components.Row(th, cells, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderPVRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	pvs := t.filteredPVs()
	if len(pvs) == 0 {
		return t.treeEmpty(c, "PVs")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW     = 1
		agW    = 5
		stW    = 8
		capW   = 7
		claimW = 24
		accW   = 6
		rclW   = 7
	)
	// Three optional columns with progressive drop priority. Order of
	// dropping (least valuable first): RECLAIM → ACCESS → CLAIM. This
	// keeps "what's binding this PV" visible the longest because that
	// is the answer the on-caller is usually looking for.
	showClaim := anyPVClaim(pvs)
	showAccess := anyPVAccessModes(pvs)
	showReclaim := anyPVReclaim(pvs)
	// computeName: returns the name-column width given the optional flags;
	// negative when there isn't room for a readable name column.
	computeName := func(claim, access, reclaim bool) int {
		count := 5 // K, NAME, STATUS, CAPACITY, AGE
		extra := 0
		if claim {
			count++
			extra += claimW
		}
		if access {
			count++
			extra += accW
		}
		if reclaim {
			count++
			extra += rclW
		}
		return tableW - kW - stW - capW - agW - extra - components.GapTotal(count)
	}
	for computeName(showClaim, showAccess, showReclaim) < 10 {
		switch {
		case showReclaim:
			showReclaim = false
		case showAccess:
			showAccess = false
		case showClaim:
			showClaim = false
		}
		if !showClaim && !showAccess && !showReclaim {
			break
		}
	}
	nW := computeName(showClaim, showAccess, showReclaim)
	if nW < 10 {
		nW = 10
	}

	cols := []int{kW, nW, stW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft}
	headers := []string{"", "NAME", "STATUS"}
	if showClaim {
		cols = append(cols, claimW)
		aligns = append(aligns, components.AlignLeft)
		headers = append(headers, "CLAIM")
	}
	cols = append(cols, capW)
	aligns = append(aligns, components.AlignRight)
	headers = append(headers, "CAPACITY")
	if showAccess {
		cols = append(cols, accW)
		aligns = append(aligns, components.AlignLeft)
		headers = append(headers, "ACCESS")
	}
	if showReclaim {
		cols = append(cols, rclW)
		aligns = append(aligns, components.AlignLeft)
		headers = append(headers, "RECLAIM")
	}
	cols = append(cols, agW)
	aligns = append(aligns, components.AlignRight)
	headers = append(headers, "AGE")
	header := components.Header(th, headers, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(pvs), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		p := pvs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(p.Name, nW), bg)
		statusCell := th.StatusGlyph(p.Status, stW, bg)
		capCell := th.MetaOn(components.TruncPad(p.Capacity, capW), bg)
		ageCell := th.Age(p.Age, bg)
		cells := []string{kindGlyph(k8s.KindPV, ""), nameCell, statusCell}
		if showClaim {
			cells = append(cells, th.MetaOn(components.TruncPad(p.Claim, claimW), bg))
		}
		cells = append(cells, capCell)
		if showAccess {
			cells = append(cells, th.MetaOn(components.TruncPad(p.AccessModes, accW), bg))
		}
		if showReclaim {
			cells = append(cells, th.MetaOn(components.TruncPad(p.Reclaim, rclW), bg))
		}
		cells = append(cells, ageCell)
		out = append(out, components.Row(th, cells, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// renderGenericRows renders instances of an enabled custom GVR fetched
// via the dynamic client. Columns: glyph · NAME · NS · STATUS · AGE. NS
// is suppressed for cluster-scoped GVRs (no namespace on any row).
func (t *Tree) renderGenericRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	items := t.filteredGeneric()
	if len(items) == 0 {
		return t.treeEmpty(c, "items")
	}
	showNS := false
	for _, r := range items {
		if r.Namespace != "" {
			showNS = true
			break
		}
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW  = 1
		stW = 10
		agW = 5
	)
	nsW := 12
	if !showNS {
		nsW = 0
	}
	colsCount := 4
	if showNS {
		colsCount = 5
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - nsW - stW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headers := []string{"", "NAME"}
	if showNS {
		cols = append(cols, nsW)
		aligns = append(aligns, components.AlignLeft)
		headers = append(headers, "NS")
	}
	cols = append(cols, stW, agW)
	aligns = append(aligns, components.AlignLeft, components.AlignRight)
	headers = append(headers, "STATUS", "AGE")
	header := components.Header(th, headers, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(items), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		r := items[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(r.Name, nW), bg)
		vals := []string{kindGlyph(k8s.KindGenericResource, ""), nameCell}
		if showNS {
			vals = append(vals, th.MetaOn(components.TruncPad(r.Namespace, nsW), bg))
		}
		vals = append(vals, th.MetaOn(components.TruncPad(r.Status, stW), bg), th.Age(r.Age, bg))
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// renderInfrastructureRows renders the children of the Infrastructure
// fold — Nodes, System (if any), and the Configure resources settings
// row. Three rows max, so we keep the layout minimal: glyph + label,
// no extra columns.
func (t *Tree) renderInfrastructureRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	rows := t.visibleRows()
	if len(rows) == 0 {
		return th.Idle.Render("(empty)")
	}
	tableW := innerW - components.EdgeTotal()
	const kW = 1
	gaps := components.GapTotal(2)
	nW := tableW - kW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	header := components.Header(th, []string{"", "NAME"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(rows), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ref := rows[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		out = append(out, components.Row(th,
			[]string{kindGlyph(ref.Kind, ""), th.NameStrongOn(components.TruncPad(ref.Display, nW), bg)},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// renderNamespaceFolderRows renders the cluster-root level: the All
// fast-path folder, every user namespace folder, and a single
// Infrastructure fold that contains the non-workload chrome. User
// namespace folders mirror the renderNamespaceRows layout so health
// pips + app counts feel consistent across drill levels.
func (t *Tree) renderNamespaceFolderRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	rows := t.visibleRows()
	if len(rows) == 0 {
		return t.treeEmpty(c, "namespaces")
	}

	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		pipW = 1
		cntW = 5
		agW  = 5
	)
	gaps := components.GapTotal(5)
	nW := tableW - kW - pipW - cntW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW, pipW, cntW, agW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignRight}
	header := components.Header(th, []string{"", "NAME", "", "APPS", "AGE"}, cols, aligns)

	// Index UserNS by name so the per-row render can look up age + cached
	// health summary without scanning the slice for every row.
	nsByName := make(map[string]k8s.NamespaceInfo, len(t.children.UserNS))
	for _, n := range t.children.UserNS {
		nsByName[n.Name] = n
	}

	start, end := components.VisibleRange(t.cursor, len(rows), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ref := rows[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)

		switch ref.Kind {
		case k8s.KindNamespaceFolder:
			ns := nsByName[ref.Name]
			nameCell := th.NameStrongOn(components.TruncPad(ref.Name, nW), bg)
			ageCell := th.Age(ns.Age, bg)
			pip, cnt := nsHealthCells(th, t.nsHealthCache[ref.Name], bg)
			out = append(out, components.Row(th,
				[]string{kindGlyph(k8s.KindNamespaceFolder, ""), nameCell, pip, cnt, ageCell},
				cols, aligns, selected, i))
		case k8s.KindAllNamespacesFold:
			// "All" is the on-caller fast path — keep it bold like the
			// namespace folders so it competes for the cursor's attention.
			vals := []string{kindGlyph(ref.Kind, ""), th.NameStrongOn(components.TruncPad(ref.Display, nW), bg)}
			for j := 2; j < len(cols); j++ {
				vals = append(vals, "")
			}
			out = append(out, components.Row(th, vals, cols, aligns, selected, i))
		case k8s.KindInfrastructureFold:
			// Single chrome row at the bottom of the cluster root —
			// dim so it sits below the namespace list as a category
			// escape hatch, not a peer of the workload namespaces.
			dim := bg.Foreground(th.P.Dim)
			vals := []string{dim.Render(kindGlyph(ref.Kind, "")), dim.Render(components.TruncPad(ref.Display, nW))}
			for j := 2; j < len(cols); j++ {
				vals = append(vals, "")
			}
			out = append(out, components.Row(th, vals, cols, aligns, selected, i))
		}
	}
	return strings.Join(out, "\n")
}

func (t *Tree) renderNamespaceRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	nss := t.filteredNamespaces()
	if len(nss) == 0 {
		return t.treeEmpty(c, "namespaces")
	}
	tableW := innerW - components.EdgeTotal()
	const (
		kW   = 1
		pipW = 1
		cntW = 5 // "(123)" worst-case width
		agW  = 5
	)
	gaps := components.GapTotal(5)
	nW := tableW - kW - pipW - cntW - agW - gaps
	if nW < 10 {
		nW = 10
	}
	cols := []int{kW, nW, pipW, cntW, agW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft, components.AlignLeft, components.AlignRight, components.AlignRight}
	header := components.Header(th, []string{"", "NAME", "", "APPS", "AGE"}, cols, aligns)

	start, end := components.VisibleRange(t.cursor, len(nss), rowsAvail-1)
	out := []string{header}
	for i := start; i < end; i++ {
		ns := nss[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		nameCell := th.NameStrongOn(components.TruncPad(ns.Name, nW), bg)
		ageCell := th.Age(ns.Age, bg)
		pip, cnt := nsHealthCells(th, t.nsHealthCache[ns.Name], bg)
		out = append(out, components.Row(th,
			[]string{kindGlyph(k8s.KindNamespace, ""), nameCell, pip, cnt, ageCell},
			cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

// nsHealthCells renders the namespace health pip + app-count cells. The
// pip is a single-cell colored circle (● Healthy=green, Degraded/InProgress=yellow,
// Failing=red, Unknown/uncached=dim ○) so the operator can scan a long
// list of namespaces and spot the unhealthy one without drilling. The
// count is dim because it's secondary to the health signal.
func nsHealthCells(th *theme.Theme, summary nsHealthSummary, bg lipgloss.Style) (pip, count string) {
	if summary.total == 0 {
		// Hollow pip + blank count read as "not visited yet" without
		// littering the screen with "?" on fresh clusters.
		pip = bg.Foreground(th.P.Dim).Render("○")
		count = ""
		return pip, count
	}
	col := th.P.Green
	switch summary.worst {
	case k8s.HealthFailing:
		col = th.P.Red
	case k8s.HealthDegraded, k8s.HealthInProgress:
		col = th.P.Yellow
	case k8s.HealthUnknown:
		col = th.P.Dim
	case k8s.HealthHealthy:
		// HealthHealthy keeps the green default.
	}
	pip = bg.Foreground(col).Bold(true).Render("●")
	count = bg.Foreground(th.P.Dim).Render(strconv.Itoa(summary.total))
	return pip, count
}

// ── Filtering / sorting ──────────────────────────────────────────────

// filteredContexts returns the kube context list with the active filter
// applied. Sorted alphabetically so muscle memory stays stable across
// launches ("prod-eu is always third").
func (t *Tree) filteredContexts() []config.ContextInfo {
	if t.children == nil {
		return nil
	}
	ctxs := append([]config.ContextInfo(nil), t.children.Contexts...)
	sort.Slice(ctxs, func(i, j int) bool { return ctxs[i].Name < ctxs[j].Name })
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(ctxs))
		for i, c := range ctxs {
			names[i] = c.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]config.ContextInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, ctxs[i])
		}
		ctxs = filtered
	}
	return ctxs
}

// renderContextRows renders the list-of-contexts level. The K-column glyph
// (⎈) doubles as the active-context marker: dim chrome FG when the row is
// just another option, green-bold when the row is the cluster the user is
// currently "in". Shape encodes kind, color encodes activity — same rule
// the rest of the app follows for health.
func (t *Tree) renderContextRows(c *tuictx.Ctx, innerW, rowsAvail int) string {
	th := c.Theme
	ctxs := t.filteredContexts()
	if len(ctxs) == 0 {
		return th.Idle.Render("no kube contexts found")
	}

	// Cluster + user columns light up only when they add information beyond
	// the context name (single-cluster setups collapse to one repeated value).
	showCluster, showUser := false, false
	for _, ci := range ctxs {
		if ci.Cluster != "" && ci.Cluster != ci.Name {
			showCluster = true
		}
		if ci.User != "" && ci.User != ci.Name {
			showUser = true
		}
	}

	tableW := innerW - components.EdgeTotal()
	const kW = 1
	colsCount := 2
	cW, uW := 0, 0
	if showCluster {
		colsCount++
		cW = 18
	}
	if showUser {
		colsCount++
		uW = 18
	}
	gaps := components.GapTotal(colsCount)
	nW := tableW - kW - cW - uW - gaps
	if nW < 10 {
		nW = 10
	}

	cols := []int{kW, nW}
	aligns := []components.Align{components.AlignLeft, components.AlignLeft}
	headerLabels := []string{"", "NAME"}
	if showCluster {
		cols = append(cols, cW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "CLUSTER-REF")
	}
	if showUser {
		cols = append(cols, uW)
		aligns = append(aligns, components.AlignLeft)
		headerLabels = append(headerLabels, "USER")
	}
	header := components.Header(th, headerLabels, cols, aligns)

	// Teach the action up-front. The panel title says "Clusters" but doesn't
	// say what to *do*; new users land on a list of names and have to guess
	// that ↵ drills in. One dim-italic line above the header carries that
	// instruction without competing visually with the rows.
	hint := th.Hint.Render("pick a cluster to begin · ↵ open")

	start, end := components.VisibleRange(t.cursor, len(ctxs), rowsAvail-2)
	out := []string{hint, header}
	for i := start; i < end; i++ {
		ci := ctxs[i]
		selected := i == t.cursor && t.Focused
		bg := th.RowBgFor(i, selected)
		// Bake bg into the glyph so the row's selection / stripe doesn't punch
		// through the foreground reset (same trick StatusGlyph uses).
		var kindCell string
		if ci.Name == c.ActiveContext {
			kindCell = bg.Foreground(th.P.Green).Bold(true).Render(kindGlyph(k8s.KindContext, ""))
		} else {
			kindCell = bg.Foreground(th.P.Dim).Render(kindGlyph(k8s.KindContext, ""))
		}
		nameCell := th.NameStrongOn(components.TruncPad(ci.Name, nW), bg)
		vals := []string{kindCell, nameCell}
		if showCluster {
			vals = append(vals, th.MetaOn(components.TruncPad(ci.Cluster, cW), bg))
		}
		if showUser {
			vals = append(vals, th.MetaOn(components.TruncPad(ci.User, uW), bg))
		}
		out = append(out, components.Row(th, vals, cols, aligns, selected, i))
	}
	return strings.Join(out, "\n")
}

func (t *Tree) filteredApps() []k8s.AppInfo {
	if t.children == nil {
		return nil
	}
	apps := t.children.Apps
	if t.alphaSort {
		apps = sortAppsAlpha(apps)
	}
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(apps))
		for i, a := range apps {
			names[i] = a.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.AppInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, apps[i])
		}
		apps = filtered
	}
	return apps
}

func sortAppsAlpha(in []k8s.AppInfo) []k8s.AppInfo {
	out := append([]k8s.AppInfo(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Tree) filteredPods() []k8s.PodInfo {
	if t.children == nil {
		return nil
	}
	pods := t.children.Pods
	if !t.alphaSort {
		pods = sortPodsHealth(pods)
	}
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pods))
		for i, p := range pods {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.PodInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pods[i])
		}
		pods = filtered
	}
	return pods
}

func sortPodsHealth(in []k8s.PodInfo) []k8s.PodInfo {
	out := append([]k8s.PodInfo(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		hi, hj := k8s.PodHealth(out[i]), k8s.PodHealth(out[j])
		if hi != hj {
			return hi < hj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (t *Tree) filteredContainers() []k8s.ContainerInfo {
	if t.children == nil {
		return nil
	}
	cs := t.children.Containers
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(cs))
		for i, c := range cs {
			names[i] = c.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.ContainerInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, cs[i])
		}
		cs = filtered
	}
	return cs
}

func (t *Tree) filteredPools() []k8s.NodePoolInfo {
	if t.children == nil {
		return nil
	}
	pools := t.children.Pools
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pools))
		for i, p := range pools {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NodePoolInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pools[i])
		}
		pools = filtered
	}
	return pools
}

func (t *Tree) filteredNodes() []k8s.NodeInfo {
	if t.children == nil {
		return nil
	}
	nodes := t.children.Nodes
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(nodes))
		for i, n := range nodes {
			names[i] = n.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NodeInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, nodes[i])
		}
		nodes = filtered
	}
	return nodes
}

func (t *Tree) filteredNamespaces() []k8s.NamespaceInfo {
	if t.children == nil {
		return nil
	}
	nss := t.children.Namespaces
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(nss))
		for i, n := range nss {
			names[i] = n.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NamespaceInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, nss[i])
		}
		nss = filtered
	}
	return nss
}

// filteredUserNamespaces is the cluster-root counterpart to filteredNamespaces:
// reads the UserNS list off Children (populated when Kind == KindNamespaceFolder)
// and applies the active filter. Kept separate so a stale filter from a prior
// drill into a sibling level can't accidentally narrow the cluster-root view.
func (t *Tree) filteredUserNamespaces() []k8s.NamespaceInfo {
	if t.children == nil {
		return nil
	}
	nss := t.children.UserNS
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(nss))
		for i, n := range nss {
			names[i] = n.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.NamespaceInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, nss[i])
		}
		nss = filtered
	}
	return nss
}

func (t *Tree) filteredConfigMaps() []k8s.ConfigMapInfo {
	if t.children == nil {
		return nil
	}
	cms := t.children.ConfigMaps
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(cms))
		for i, cm := range cms {
			names[i] = cm.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.ConfigMapInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, cms[i])
		}
		cms = filtered
	}
	return cms
}

func (t *Tree) filteredServices() []k8s.ServiceInfo {
	if t.children == nil {
		return nil
	}
	svcs := t.children.Services
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(svcs))
		for i, s := range svcs {
			names[i] = s.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.ServiceInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, svcs[i])
		}
		svcs = filtered
	}
	return svcs
}

func (t *Tree) filteredIngresses() []k8s.IngressInfo {
	if t.children == nil {
		return nil
	}
	ings := t.children.Ingresses
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(ings))
		for i, ing := range ings {
			names[i] = ing.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.IngressInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, ings[i])
		}
		ings = filtered
	}
	return ings
}

func (t *Tree) filteredSecrets() []k8s.SecretInfo {
	if t.children == nil {
		return nil
	}
	secrets := t.children.Secrets
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(secrets))
		for i, s := range secrets {
			names[i] = s.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.SecretInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, secrets[i])
		}
		secrets = filtered
	}
	return secrets
}

func (t *Tree) filteredPVCs() []k8s.PVCInfo {
	if t.children == nil {
		return nil
	}
	pvcs := t.children.PVCs
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pvcs))
		for i, p := range pvcs {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.PVCInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pvcs[i])
		}
		pvcs = filtered
	}
	return pvcs
}

func (t *Tree) filteredPVs() []k8s.PVInfo {
	if t.children == nil {
		return nil
	}
	pvs := t.children.PVs
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(pvs))
		for i, p := range pvs {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.PVInfo, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, pvs[i])
		}
		pvs = filtered
	}
	return pvs
}

func (t *Tree) filteredGeneric() []k8s.GenericResource {
	if t.children == nil {
		return nil
	}
	items := t.children.Generic
	if q := t.filter.Value(); q != "" {
		names := make([]string, len(items))
		for i, p := range items {
			names[i] = p.Name
		}
		idx := components.FuzzyMatch(q, names)
		filtered := make([]k8s.GenericResource, 0, len(idx))
		for _, i := range idx {
			filtered = append(filtered, items[i])
		}
		items = filtered
	}
	return items
}

func shortSecretType(t string) string {
	switch t {
	case "Opaque":
		return "Opaque"
	case "kubernetes.io/tls":
		return "TLS"
	case "kubernetes.io/dockerconfigjson":
		return "docker"
	case "kubernetes.io/basic-auth":
		return "basic"
	case "kubernetes.io/ssh-auth":
		return "SSH"
	case "kubernetes.io/service-account-token":
		return "sa-token"
	case "bootstrap.kubernetes.io/token":
		return "bootstrap"
	default:
		if len(t) > 16 {
			return t[:13] + "..."
		}
		return t
	}
}

func (t *Tree) firstUnhealthyIndex() int {
	rows := t.visibleRows()
	for i, r := range rows {
		if r.Health < k8s.HealthHealthy {
			return i
		}
	}
	return t.cursor
}

// ── Glyphs ───────────────────────────────────────────────────────────
//
// The glyph system is organized as visual families so the kind of any row
// is recognisable at a glance — the eye learns the family, not 12 unrelated
// shapes. Every glyph is a single-cell BMP character (no emoji), so column
// alignment and breadcrumb width math stay honest across terminals.
//
// Family A — Workloads (diamonds + clock + half-circle)
//   ◇ Deployment   hollow  → "anonymous replicas, replaceable"
//   ◆ StatefulSet  filled  → "identity-bound replicas"   (hollow→filled
//                                                          encodes identity)
//   ◷ CronJob      clock-quadrant → "time-triggered"
//   ◐ Job          half-filled    → "one-shot, mid-run"
//   ▦ DaemonSet    cross-hatched square → "one-per-node, infra-adjacent"
//                                          (intentionally crosses into the
//                                          square family — DaemonSets are
//                                          workloads BUT node-bound, so the
//                                          glyph carries that duality)
//
// Family B — Infrastructure (squares, hierarchically zoomed)
//   ▣ ClusterFold  square-in-square  → "the whole cluster — frame holding units"
//   ▥ NodePool     vertical-fill     → "a rack: many vertical bars"
//   □ Node         single empty box  → "one unit"
//   ⊟ SystemFold   collapsed         → "hidden / aggregated"
//
// Family C — Scopes & runtime (distinct, single-purpose)
//   ⌂ ContextList  home              → "root / picker"
//   ⎈ Context      Kubernetes helm   → "a cluster" (real k8s mark)
//   ▢ Namespace    rounded square    → "logical scope" (visually distinct
//                                       from family B's sharp squares)
//   ◉ Pod          fisheye circle    → "an instance with internal state"
//                                       (distinct from `●` so the pod kind
//                                       glyph doesn't clash with the colored
//                                       status dot in the STATUS column)
//   ▾ Container    small triangle    → "drill-disclosure: a piece inside"
//
// Family D — Resource folds (squared operators, U+229E–U+22A1)
//   ⊟ SystemFold   squared minus  → "collapsed: hidden namespaces"
//   ⊞ ConfigFold   squared plus   → "settings to add"
//   ⊠ SecretsFold  squared times  → "sealed / X-ed out"
//   ⊜ NetworkFold  squared equal  → "balanced flow / connections"
//   ⊡ StorageFold  squared dot    → "block of data"
//
// Family E — Resource leaves (single-cell, no emoji)
//   ▤ ConfigMap    horizontal-fill   → "rows of key=value"
//   ⌧ Secret       X in rectangle    → "redacted"
//   ◫ Service      vertical-bar box  → "endpoint port"
//   ⇥ Ingress      arrow to bar      → "into the cluster"
//   ◰ PVC          quadrant top-left → "claim issued"
//   ◲ PV           quadrant bot-right→ "volume bound"  (◰→◲ encodes binding)
//
// Indicator (not a kind):
//   ↕ HPA badge — vertical scale arrows on App rows

func appKindGlyph(kind string) string {
	switch kind {
	case k8s.AppKindDeployment:
		return "◇"
	case k8s.AppKindStatefulSet:
		return "◆"
	case k8s.AppKindDaemonSet:
		return "▦"
	case k8s.AppKindCronJob:
		return "◷"
	case k8s.AppKindJob:
		return "◐"
	}
	return "·"
}

func kindGlyph(kind, sub string) string {
	switch kind {
	case k8s.KindContextList:
		return "⌂"
	case k8s.KindContext:
		return "⎈"
	case k8s.KindApp:
		return appKindGlyph(sub)
	case k8s.KindPod:
		return "◉"
	case k8s.KindContainer:
		return "▾"
	case k8s.KindClusterFold:
		return "▣"
	case k8s.KindNodePool:
		return "▥"
	case k8s.KindNode:
		return "□"
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return "▢"
	case k8s.KindSystemFold:
		return "⊟"
	case k8s.KindAllNamespacesFold:
		// Asterisk-operator: "all", visually distinct from the squared-
		// operator fold family so the on-caller can pick it out as a
		// special-case sibling next to the named namespace folders.
		return "✻"
	case k8s.KindWorkloadsFold:
		// Squared-plus operator family — pairs visually with the other
		// resource folds (⊞ Config, ⊠ Secrets, ⊜ Network, ⊡ Storage,
		// ⊟ System). ⊕ reads as "all the workloads added together".
		return "⊕"
	case k8s.KindHPA:
		return "↕"
	case k8s.KindConfigFold:
		return "⊞"
	case k8s.KindSecretsFold:
		return "⊠"
	case k8s.KindNetworkFold:
		return "⊜"
	case k8s.KindIngressFold:
		// Reuse the Ingress leaf glyph (⇥) so the fold and its children
		// read as the same concept. Off-family from the squared-operator
		// folds but distinct enough that the eye doesn't confuse it with
		// Services (⊜).
		return "⇥"
	case k8s.KindStorageFold:
		return "⊡"
	case k8s.KindConfigMap:
		return "▤"
	case k8s.KindSecret:
		return "⌧"
	case k8s.KindService:
		return "◫"
	case k8s.KindIngress:
		return "⇥"
	case k8s.KindPVC:
		return "◰"
	case k8s.KindPV:
		return "◲"
	case k8s.KindCustomFold:
		// Diamond — outside the squared-operator fold family so the eye
		// reads "this is a non-default fold, beyond the safina built-ins".
		// Distinct enough from the App workload glyphs.
		return "◇"
	case k8s.KindGenericResource:
		// Diamond bullet for an individual instance of a custom GVR.
		return "◈"
	case k8s.KindInfrastructureFold:
		// Stacked-bars glyph: "the pile of cluster-internal stuff" —
		// Nodes, System namespaces, Configure resources. Reads as a
		// folder of meta things, distinct from the squared-operator
		// resource folds inside namespaces.
		return "▦"
	}
	return ""
}

func healthGlyphColored(t *theme.Theme, h k8s.Health, bg lipgloss.Style) string {
	return healthGlyphStyled(t, h, bg, false)
}

// healthGlyphStyled renders the health glyph with optional "pulse"
// emphasis. When pulsing is true, the glyph reverses its colors so the
// row visually pops on the next redraw — this is how we surface a
// just-changed-state without animation infrastructure. The pulse decays
// after pulseDuration; the renderer simply asks the tree if a row is
// pulsing on each frame.
func healthGlyphStyled(t *theme.Theme, h k8s.Health, bg lipgloss.Style, pulsing bool) string {
	// Suppress the affirmation glyph on healthy rows. On a calm cluster
	// that's ~every row, and a column full of green ✓'s reads as visual
	// noise rather than signal — exactly the rationale behind suppressing
	// "Running" on the status glyph. The H column stays mostly empty; any
	// shape that appears (✗ ⚠ ⏵ …) is, by definition, an outlier worth
	// landing the eye on. The bg-painted blank preserves the row stripe.
	// Pulse still fires on Healthy so a *transition* into healthy briefly
	// flashes — operators see the "it came back" moment.
	if h == k8s.HealthHealthy && !pulsing {
		return bg.Render(" ")
	}
	g := h.Glyph()
	style := bg.Foreground(t.P.Dim)
	switch h {
	case k8s.HealthHealthy:
		style = bg.Foreground(t.P.Green).Bold(true)
	case k8s.HealthDegraded:
		style = bg.Foreground(t.P.Yellow).Bold(true)
	case k8s.HealthFailing:
		style = bg.Foreground(t.P.Red).Bold(true)
	case k8s.HealthInProgress:
		style = bg.Foreground(t.P.Blue).Bold(true)
	case k8s.HealthUnknown:
	}
	if pulsing {
		// Reverse-video keeps the same hue family but inverts fg/bg so
		// the glyph reads as "this just changed". Adds Blink for
		// terminals that honor it (most modern ones don't, which is
		// fine — the reverse alone is enough signal).
		style = style.Reverse(true).Blink(true)
	}
	return style.Render(g)
}

func shortKind(kind string) string {
	switch kind {
	case "regular":
		return "reg"
	case "init":
		return "init"
	case "ephemeral":
		return "eph"
	}
	return "—"
}

func clonePath(p []NodeRef) []NodeRef {
	out := make([]NodeRef, len(p))
	copy(out, p)
	return out
}
