package sections

import (
	"cmp"
	"slices"
	"time"
)

const (
	maxDetailCacheEntries     = 256
	maxTreeCacheEntries       = 128
	maxNavigationPositions    = 512
	maxHealthEntries          = 8192
	maxNamespaceHealthEntries = 4096
	maxTreeCachedRows         = 50000
)

type cacheRecency struct {
	clock uint64
	seen  map[string]uint64
}

func (r *cacheRecency) touch(key string) {
	if r.seen == nil {
		r.seen = make(map[string]uint64)
	}
	r.clock++
	r.seen[key] = r.clock
}

// Eviction removes ownership of the payload, not merely its lookup label.
// Active breadcrumb levels may be protected so Esc remains instant.
func pruneCache[T any](entries map[string]T, r *cacheRecency, limit int, protect func(string) bool) []string {
	for key := range r.seen {
		if _, ok := entries[key]; !ok {
			delete(r.seen, key)
		}
	}
	if len(entries) <= limit {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		if protect == nil || !protect(key) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(r.seen[a], r.seen[b]), cmp.Compare(a, b)) })
	n := min(len(keys), len(entries)-limit)
	for _, key := range keys[:n] {
		delete(entries, key)
		delete(r.seen, key)
	}
	return keys[:n]
}

func (t *Tree) protectedLevel(key string) bool {
	for n := 1; n <= len(t.path); n++ {
		if pathKeyFor(t.path[:n]) == key {
			return true
		}
	}
	return false
}

func (t *Tree) cachedLevel(key string) *Children {
	ch := t.cache[key]
	if ch != nil {
		t.levelRecency.touch(key)
	}
	return ch
}

func (t *Tree) cacheLevel(key string, ch *Children) {
	t.cache[key] = ch
	t.levelRecency.touch(key)
	pruneCache(t.cache, &t.levelRecency, maxTreeCacheEntries, t.protectedLevel)
	for t.cachedRowCount() > maxTreeCachedRows {
		if len(pruneCache(t.cache, &t.levelRecency, len(t.cache)-1, t.protectedLevel)) == 0 {
			break
		}
	}
}

func (t *Tree) cachedRowCount() int {
	n := 0
	for _, ch := range t.cache {
		if ch == nil {
			continue
		}
		n += len(ch.Contexts) + len(ch.Apps) + len(ch.Pods) + len(ch.Containers) + len(ch.Pools) + len(ch.Nodes) + len(ch.Namespaces) + len(ch.ConfigMaps) + len(ch.Secrets) + len(ch.Services) + len(ch.Ingresses) + len(ch.PVCs) + len(ch.PVs) + len(ch.Generic) + len(ch.UserNS) + len(ch.SystemNS)
	}
	return n
}

func (t *Tree) rememberCursor() {
	key := t.pathKey()
	t.cursorByKey[key] = t.cursor
	t.positionRecency.touch(key)
	for _, evicted := range pruneCache(t.cursorByKey, &t.positionRecency, maxNavigationPositions, t.protectedLevel) {
		delete(t.resourceFilters, evicted)
	}
}

func (t *Tree) pruneHealthRetention() {
	limit := maxHealthEntries
	if len(t.lastHealth) > limit {
		limit = limit * 9 / 10
	}
	for _, key := range pruneCache(t.lastHealth, &t.healthRecency, limit, nil) {
		delete(t.healthPulse, key)
	}
	for key, at := range t.healthPulse {
		if time.Since(at) >= pulseDuration {
			delete(t.healthPulse, key)
		}
	}
	pruneCache(t.nsHealthCache, &t.namespaceRecency, maxNamespaceHealthEntries, nil)
}
