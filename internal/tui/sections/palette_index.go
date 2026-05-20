package sections

import (
	"github.com/yunus/safina/internal/k8s"
)

// PaletteEntry is one row in the command-palette result list. A flat,
// pre-rendered shape lets the fuzzy matcher score everything in a single
// pass without re-walking the tree's nested cache.
type PaletteEntry struct {
	Kind      string // k8s.Kind* (App, Pod, Secret, …)
	SubKind   string // app sub-kind for KindApp; "" otherwise
	Namespace string // empty for cluster-scoped resources
	Name      string
	// Path is the breadcrumb stack the tree should drill to in order to
	// land the cursor on this entry. Built once during indexing so the
	// palette doesn't need to know how the tree navigates.
	Path []NodeRef
	// Hint is short status text shown next to the row in the result list
	// (e.g. "● Healthy 3/3", "Running", "8 keys"). Pre-rendered so the
	// fuzzy match doesn't have to ignore styling bytes.
	Hint string
}

// SearchString is what the fuzzy matcher scores against. We include the
// kind and namespace as suffix tokens so queries like "secret tls" or
// "pod shop" naturally narrow even before the matcher runs.
func (e PaletteEntry) SearchString() string {
	parts := e.Name
	if e.Namespace != "" {
		parts += " · " + e.Namespace
	}
	if e.SubKind != "" {
		parts += " · " + e.SubKind
	} else {
		parts += " · " + e.Kind
	}
	return parts
}

// BuildPaletteIndex walks the tree's level cache and produces a flat slice
// of palette-searchable entries from every resource the user has visited
// so far in the session. The palette gets richer the deeper into the
// cluster the operator drills — that's intentional: we don't pre-fetch
// the whole cluster on every `:` press, which would be slow and would
// surface stale data on long-running sessions.
func BuildPaletteIndex(cache map[string]*Children) []PaletteEntry {
	out := make([]PaletteEntry, 0, 256)
	seen := make(map[string]struct{}, 256)

	for path, ch := range cache {
		if ch == nil {
			continue
		}
		// Reconstruct the parent path from the cache key. The cache key is
		// our pathKey; we don't have the original NodeRef slice handy, so
		// each entry's drill-target Path is computed below via composeChild.
		parentPath := decodePathKey(path)

		for _, c := range ch.Contexts {
			add(&out, seen, PaletteEntry{
				Kind: k8s.KindContext,
				Name: c.Name,
				Path: composeChild(parentPath, NodeRef{Kind: k8s.KindContext, Name: c.Name, Display: c.Name}),
				Hint: "cluster",
			})
		}
		for _, ns := range ch.Namespaces {
			add(&out, seen, PaletteEntry{
				Kind: k8s.KindNamespace,
				Name: ns.Name,
				Path: composeChild(parentPath, NodeRef{Kind: k8s.KindNamespace, Name: ns.Name, Display: ns.Name}),
				Hint: "ns",
			})
		}
		for _, ns := range ch.UserNS {
			add(&out, seen, PaletteEntry{
				Kind: k8s.KindNamespaceFolder,
				Name: ns.Name,
				Path: composeChild(parentPath, NodeRef{Kind: k8s.KindNamespaceFolder, Namespace: ns.Name, Name: ns.Name, Display: ns.Name}),
				Hint: "ns",
			})
		}
		for _, a := range ch.Apps {
			ref := NodeRef{Kind: k8s.KindApp, SubKind: a.Kind, Namespace: a.Namespace, Name: a.Name, Display: a.Name, UID: a.UID}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindApp,
				SubKind:   a.Kind,
				Namespace: a.Namespace,
				Name:      a.Name,
				Path:      composeChild(parentPath, ref),
				Hint:      a.Ready,
			})
		}
		for _, p := range ch.Pods {
			ref := NodeRef{Kind: k8s.KindPod, Namespace: p.Namespace, Name: p.Name, Display: p.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindPod,
				Namespace: p.Namespace,
				Name:      p.Name,
				Path:      composeChild(parentPath, ref),
				Hint:      p.Status,
			})
		}
		for _, cm := range ch.ConfigMaps {
			ref := NodeRef{Kind: k8s.KindConfigMap, Namespace: cm.Namespace, Name: cm.Name, Display: cm.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindConfigMap,
				Namespace: cm.Namespace,
				Name:      cm.Name,
				Path:      composeChild(parentPath, ref),
			})
		}
		for _, s := range ch.Secrets {
			ref := NodeRef{Kind: k8s.KindSecret, Namespace: s.Namespace, Name: s.Name, Display: s.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindSecret,
				Namespace: s.Namespace,
				Name:      s.Name,
				Path:      composeChild(parentPath, ref),
			})
		}
		for _, sv := range ch.Services {
			ref := NodeRef{Kind: k8s.KindService, Namespace: sv.Namespace, Name: sv.Name, Display: sv.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindService,
				Namespace: sv.Namespace,
				Name:      sv.Name,
				Path:      composeChild(parentPath, ref),
				Hint:      sv.Type,
			})
		}
		for _, ing := range ch.Ingresses {
			ref := NodeRef{Kind: k8s.KindIngress, Namespace: ing.Namespace, Name: ing.Name, Display: ing.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindIngress,
				Namespace: ing.Namespace,
				Name:      ing.Name,
				Path:      composeChild(parentPath, ref),
			})
		}
		for _, pvc := range ch.PVCs {
			ref := NodeRef{Kind: k8s.KindPVC, Namespace: pvc.Namespace, Name: pvc.Name, Display: pvc.Name}
			add(&out, seen, PaletteEntry{
				Kind:      k8s.KindPVC,
				Namespace: pvc.Namespace,
				Name:      pvc.Name,
				Path:      composeChild(parentPath, ref),
				Hint:      pvc.Status,
			})
		}
		for _, n := range ch.Nodes {
			ref := NodeRef{Kind: k8s.KindNode, Name: n.Name, Display: n.Name, UID: n.UID}
			add(&out, seen, PaletteEntry{
				Kind: k8s.KindNode,
				Name: n.Name,
				Path: composeChild(parentPath, ref),
				Hint: n.Status,
			})
		}
	}
	return out
}

// add appends an entry if its identity hasn't already been recorded. The
// same resource can sit in two cache levels (e.g. a Service appears under
// the namespace's Apps view AND under the Network fold) — without dedup
// the palette would surface duplicate rows.
func add(out *[]PaletteEntry, seen map[string]struct{}, e PaletteEntry) {
	id := e.Kind + "/" + e.Namespace + "/" + e.Name
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	*out = append(*out, e)
}

// composeChild builds a child path by appending a single ref to the
// parent. Returns a copy so callers can safely retain the slice.
func composeChild(parent []NodeRef, child NodeRef) []NodeRef {
	out := make([]NodeRef, 0, len(parent)+1)
	out = append(out, parent...)
	out = append(out, child)
	return out
}

// decodePathKey reverses pathKeyFor (in tree.go) so we can reconstruct a
// parent path from a cache map key. Format is "Kind/Namespace/Name/UID"
// segments joined by "|".
func decodePathKey(key string) []NodeRef {
	if key == "" {
		return nil
	}
	segs := splitOn(key, '|')
	out := make([]NodeRef, 0, len(segs))
	for _, seg := range segs {
		parts := splitOn(seg, '/')
		if len(parts) < 4 {
			continue
		}
		out = append(out, NodeRef{
			Kind:      parts[0],
			Namespace: parts[1],
			Name:      parts[2],
			Display:   parts[2],
		})
	}
	return out
}

func splitOn(s string, sep byte) []string {
	out := []string{}
	start := 0
	for i := range len(s) {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
