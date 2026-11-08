package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
)

// ── decodePathKey / composeChild / add ────────────────────────────────

func TestDecodePathKey_Empty(t *testing.T) {
	assert.Nil(t, decodePathKey(""))
}

func TestDecodePathKey_RoundTripsSegmentsToRefs(t *testing.T) {
	// pathKeyFor emits "<Kind>/<Ns>/<Name>/<UID>/<GVR>" joined by "|".
	// decodePathKey should recover Kind/Ns/Name (UID and GVR are dropped —
	// that's intentional, the palette only needs them for drill-target
	// reconstruction).
	key := "Context//prod-eu-1//|Namespace//shop//|App/shop/checkout/uid-1/apps/v1/deployments"
	refs := decodePathKey(key)
	require.Len(t, refs, 3)

	assert.Equal(t, "Context", refs[0].Kind)
	assert.Equal(t, "prod-eu-1", refs[0].Name)
	assert.Equal(t, "prod-eu-1", refs[0].Display)
	assert.Empty(t, refs[0].Namespace)

	assert.Equal(t, "Namespace", refs[1].Kind)
	assert.Equal(t, "shop", refs[1].Name)

	assert.Equal(t, k8s.KindApp, refs[2].Kind)
	assert.Equal(t, "shop", refs[2].Namespace)
	assert.Equal(t, "checkout", refs[2].Name)
}

func TestDecodePathKey_SkipsMalformedSegments(t *testing.T) {
	// Anything with fewer than 4 slash-separated parts is dropped — that's
	// the safety net for "version skew rewrote pathKeyFor" not corrupting
	// the palette index.
	key := "broken|Context//prod//|alsobroken/x/y" // 2nd has < 4 parts
	refs := decodePathKey(key)
	require.Len(t, refs, 1, "only the well-formed segment should survive")
	assert.Equal(t, "Context", refs[0].Kind)
	assert.Equal(t, "prod", refs[0].Name)
}

func TestComposeChild_ReturnsCopyNotAlias(t *testing.T) {
	parent := []NodeRef{{Kind: k8s.KindContext, Name: "a"}}
	child := NodeRef{Kind: k8s.KindNamespace, Name: "b"}
	got := composeChild(parent, child)
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Name)
	assert.Equal(t, "b", got[1].Name)

	// Mutating the returned slice must not affect parent — the palette
	// relies on this when the same parent path feeds many children, and a
	// bug here would entangle distinct entries' drill targets.
	got[0].Name = "MUTATED"
	assert.Equal(t, "a", parent[0].Name, "composeChild must return an independent slice")
}

func TestAdd_DeduplicatesByKindNamespaceName(t *testing.T) {
	out := []PaletteEntry{}
	seen := map[string]struct{}{}
	add(&out, seen, PaletteEntry{Kind: k8s.KindPod, Namespace: "ns", Name: "p"})
	add(&out, seen, PaletteEntry{Kind: k8s.KindPod, Namespace: "ns", Name: "p"})
	add(&out, seen, PaletteEntry{Kind: k8s.KindPod, Namespace: "ns", Name: "q"})
	// Same name, different namespace is a different entry.
	add(&out, seen, PaletteEntry{Kind: k8s.KindPod, Namespace: "ns2", Name: "p"})
	// Same name + namespace, different kind is a different entry.
	add(&out, seen, PaletteEntry{Kind: k8s.KindService, Namespace: "ns", Name: "p"})
	require.Len(t, out, 4, "dedup must key on (Kind,Namespace,Name)")
}

// ── BuildPaletteIndex ─────────────────────────────────────────────────

func TestBuildPaletteIndex_EmptyCache(t *testing.T) {
	assert.Empty(t, BuildPaletteIndex(nil))
	assert.Empty(t, BuildPaletteIndex(map[string]*Children{}))
}

func TestBuildPaletteIndex_SkipsNilChildren(t *testing.T) {
	// A cache entry with a nil Children pointer (e.g. cleared after a
	// context switch) must not crash the indexer.
	cache := map[string]*Children{"": nil}
	require.NotPanics(t, func() { BuildPaletteIndex(cache) })
}

func TestBuildPaletteIndex_AssemblesEntries(t *testing.T) {
	cache := map[string]*Children{
		"": {
			Kind:     k8s.KindContextList,
			Contexts: []config.ContextInfo{{Name: "prod-eu-1"}},
		},
		"Context//prod-eu-1//": {
			Kind: k8s.KindNamespaceFolder,
			UserNS: []k8s.NamespaceInfo{
				{Name: "shop"},
			},
		},
		"Context//prod-eu-1//|NamespaceFolder/shop/shop//": {
			Kind: k8s.KindApp,
			Apps: []k8s.AppInfo{
				{Kind: k8s.AppKindDeployment, Namespace: "shop", Name: "checkout", Ready: "3/3"},
			},
			Pods: []k8s.PodInfo{
				{Namespace: "shop", Name: "checkout-0", Status: "Running"},
			},
			Services: []k8s.ServiceInfo{
				{Namespace: "shop", Name: "checkout", Type: "ClusterIP"},
			},
			Secrets: []k8s.SecretInfo{
				{Namespace: "shop", Name: "tls-cert"},
			},
		},
	}
	entries := BuildPaletteIndex(cache)
	// Index by (kind,ns,name) for assertions; iteration order over a
	// Go map isn't deterministic so we don't pin positions.
	byID := map[string]PaletteEntry{}
	for _, e := range entries {
		byID[e.Kind+"/"+e.Namespace+"/"+e.Name] = e
	}

	require.Contains(t, byID, k8s.KindContext+"//prod-eu-1")
	require.Contains(t, byID, k8s.KindNamespaceFolder+"/shop/shop")
	app := byID[k8s.KindApp+"/shop/checkout"]
	require.Equal(t, k8s.AppKindDeployment, app.SubKind)
	require.Equal(t, "3/3", app.Hint)
	require.Equal(t, "shop", app.Namespace)
	require.Equal(t, k8s.KindWorkloadsFold, app.Path[len(app.Path)-2].Kind)

	require.Contains(t, byID, k8s.KindPod+"/shop/checkout-0")
	require.Equal(t, "Running", byID[k8s.KindPod+"/shop/checkout-0"].Hint)

	require.Equal(t, "ClusterIP", byID[k8s.KindService+"/shop/checkout"].Hint)

	// Secret has no hint field populated by indexer.
	require.Contains(t, byID, k8s.KindSecret+"/shop/tls-cert")
}

func TestBuildPaletteIndex_AppDrillTargetCarriesSubKind(t *testing.T) {
	// Drilling into an App from the palette uses the path Path. The path's
	// last NodeRef MUST carry SubKind — without it the tree can't render
	// the right glyph or fetch the right resource (Deployment vs StatefulSet).
	cache := map[string]*Children{
		"": {
			Kind: k8s.KindApp,
			Apps: []k8s.AppInfo{{
				Kind: k8s.AppKindStatefulSet, Namespace: "db",
				Name: "postgres", Ready: "1/1",
			}},
		},
	}
	entries := BuildPaletteIndex(cache)
	require.Len(t, entries, 1)
	e := entries[0]
	require.NotEmpty(t, e.Path)
	last := e.Path[len(e.Path)-1]
	assert.Equal(t, k8s.KindApp, last.Kind)
	assert.Equal(t, k8s.AppKindStatefulSet, last.SubKind,
		"App ref must carry SubKind so the tree fetches the right kind")
	assert.Equal(t, "db", last.Namespace)
	assert.Equal(t, "postgres", last.Name)
}

func TestBuildPaletteIndex_DedupsAcrossCacheLevels(t *testing.T) {
	// The same Service can sit under both a namespace's Services list AND
	// the namespace's NetworkFold view. Without dedup the palette shows
	// the same row twice — confusing and dishonest.
	svc := k8s.ServiceInfo{Namespace: "shop", Name: "checkout", Type: "ClusterIP"}
	cache := map[string]*Children{
		"a": {Services: []k8s.ServiceInfo{svc}},
		"b": {Services: []k8s.ServiceInfo{svc}},
	}
	entries := BuildPaletteIndex(cache)
	count := 0
	for _, e := range entries {
		if e.Kind == k8s.KindService && e.Name == "checkout" && e.Namespace == "shop" {
			count++
		}
	}
	assert.Equal(t, 1, count, "duplicate Service from two cache levels must dedup")
}

// ── PaletteEntry.SearchString ─────────────────────────────────────────

func TestPaletteEntry_SearchString_IncludesKindAndNamespace(t *testing.T) {
	// Operators search like "secret tls" or "pod checkout" — relying on
	// the SearchString carrying the kind/namespace as suffix tokens. The
	// fuzzy matcher scores against this string.
	e := PaletteEntry{Kind: k8s.KindSecret, Namespace: "shop", Name: "tls-cert"}
	got := e.SearchString()
	assert.Contains(t, got, "tls-cert")
	assert.Contains(t, got, "shop")
	assert.Contains(t, got, "Secret")
}

func TestPaletteEntry_SearchString_AppUsesSubKindNotKind(t *testing.T) {
	// An App entry should expose the concrete sub-kind ("Deployment",
	// "CronJob") so the user's query disambiguates between kinds — typing
	// "cron checkout" should land on the CronJob, not other Apps.
	e := PaletteEntry{
		Kind: k8s.KindApp, SubKind: k8s.AppKindCronJob,
		Namespace: "billing", Name: "rollup",
	}
	got := e.SearchString()
	assert.Contains(t, got, k8s.AppKindCronJob)
	assert.NotContains(t, got, " · App ",
		"the generic 'App' kind tag must not appear when SubKind is set")
}

func TestPaletteEntry_SearchString_OmitsNamespaceWhenCluster(t *testing.T) {
	// Cluster-scoped entries (Nodes) have no Namespace — must not produce
	// a stray "· " token (the suffix separator).
	e := PaletteEntry{Kind: k8s.KindNode, Name: "gke-1"}
	got := e.SearchString()
	assert.NotContains(t, got, "·  ·", "no double separator when ns is empty")
}

// ── Palette query behavior ────────────────────────────────────────────

func sampleEntries() []PaletteEntry {
	return []PaletteEntry{
		{Kind: k8s.KindPod, Namespace: "shop", Name: "checkout-0", Hint: "Running"},
		{Kind: k8s.KindPod, Namespace: "shop", Name: "checkout-1", Hint: "Running"},
		{Kind: k8s.KindService, Namespace: "shop", Name: "checkout", Hint: "ClusterIP"},
		{Kind: k8s.KindSecret, Namespace: "shop", Name: "tls-cert"},
		{Kind: k8s.KindPod, Namespace: "billing", Name: "ledger-0", Hint: "Running"},
	}
}

func TestPalette_OpenInitialisesEmptyQuery(t *testing.T) {
	p := NewPalette()
	p.Open(sampleEntries())
	assert.Empty(t, p.Query())
	// All entries (under the head cap of 50) surface as matches.
	assert.Len(t, p.matches, 5)
	sel, ok := p.Selected()
	assert.True(t, ok)
	assert.Equal(t, "checkout-0", sel.Name)
}

func TestPalette_SetQueryRanksMatches(t *testing.T) {
	p := NewPalette()
	p.Open(sampleEntries())
	p.SetQuery("checkout")
	require.NotEmpty(t, p.matches)
	// All matches must contain "checkout" in their search string.
	for _, m := range p.matches {
		assert.Contains(t, m.SearchString(), "checkout",
			"every match must actually match the query")
	}
}

func TestPalette_SetQueryFiltersOutNonMatches(t *testing.T) {
	p := NewPalette()
	p.Open(sampleEntries())
	p.SetQuery("tls")
	require.NotEmpty(t, p.matches)
	for _, m := range p.matches {
		assert.NotEqual(t, k8s.KindPod, m.Kind,
			"query 'tls' must not surface a Pod row")
	}
}

func TestPalette_SetQueryResetsCursor(t *testing.T) {
	p := NewPalette()
	p.Open(sampleEntries())
	p.CursorDown()
	p.CursorDown()
	require.Equal(t, 2, p.cursor)
	p.SetQuery("ledger")
	assert.Equal(t, 0, p.cursor, "SetQuery must reset cursor — old cursor often points past the new match list")
}

func TestPalette_CursorBoundsWhenNoMatches(t *testing.T) {
	// Empty result list — Selected returns ok=false, cursor moves do nothing.
	p := NewPalette()
	p.Open(sampleEntries())
	p.SetQuery("zzzzz-no-such-thing")
	require.Empty(t, p.matches)
	require.NotPanics(t, func() { p.CursorUp(); p.CursorDown() })
	_, ok := p.Selected()
	assert.False(t, ok)
}

func TestPalette_CursorClampsToBounds(t *testing.T) {
	p := NewPalette()
	p.Open(sampleEntries())
	// Up at top is a no-op.
	p.CursorUp()
	assert.Equal(t, 0, p.cursor)
	// Down past bottom clamps to last.
	for range 100 {
		p.CursorDown()
	}
	assert.Equal(t, len(p.matches)-1, p.cursor)
}

func TestPalette_VerbHitIsConsumedOnce(t *testing.T) {
	// SetVerbHit + VerbHit is a one-shot — consuming a verb must clear it
	// so the next tick doesn't re-fire the same action. Caller relies on
	// "" meaning "no verb pressed".
	p := NewPalette()
	p.Open(sampleEntries())
	assert.Empty(t, p.VerbHit())
	p.SetVerbHit("l")
	assert.Equal(t, "l", p.VerbHit())
	assert.Empty(t, p.VerbHit(), "second read must return empty — verb already consumed")
}

func TestPalette_OpenResetsState(t *testing.T) {
	// User closes and re-opens the palette: query, cursor, and pending
	// verb must all reset so the new session starts clean.
	p := NewPalette()
	p.Open(sampleEntries())
	p.SetQuery("checkout")
	p.CursorDown()
	p.SetVerbHit("l")
	p.Open(sampleEntries()) // re-open
	assert.Empty(t, p.Query())
	assert.Equal(t, 0, p.cursor)
	assert.Empty(t, p.VerbHit())
}

func TestPalette_EmptyQueryKeepsAllLoadedResources(t *testing.T) {
	// Rendering is windowed; scrolling must still reach every loaded entry.
	many := make([]PaletteEntry, 0, 120)
	for range 120 {
		many = append(many, PaletteEntry{Kind: k8s.KindPod, Name: "p"})
	}
	p := NewPalette()
	p.Open(many)
	assert.Len(t, p.matches, 120)
}

// ── PaletteVerbsFor ───────────────────────────────────────────────────

func TestPaletteVerbsFor_DeploymentHasMutators(t *testing.T) {
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment})
	letters := verbLetters(verbs)
	for _, want := range []string{"d", "l", "e", "S", "U", "R", "E", "K"} {
		assert.Contains(t, letters, want, "deployment must expose %q", want)
	}
}

func TestPaletteVerbsFor_StatefulSetHasNoUndo(t *testing.T) {
	// Per the keymap convention, only Deployments expose Undo. A
	// regression that added U to StatefulSet would silently dispatch a
	// kubectl rollout undo on a kind that doesn't support it.
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindApp, SubKind: k8s.AppKindStatefulSet})
	letters := verbLetters(verbs)
	assert.NotContains(t, letters, "U", "StatefulSet must not expose Undo")
}

func TestPaletteVerbsFor_DaemonSetHasNoScale(t *testing.T) {
	// DaemonSet replicas are node-driven; scale doesn't apply.
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindApp, SubKind: k8s.AppKindDaemonSet})
	letters := verbLetters(verbs)
	assert.NotContains(t, letters, "S", "DaemonSet must not expose Scale")
}

func TestPaletteVerbsFor_JobAndCronJobOnlyExposeReadVerbsAndDelete(t *testing.T) {
	for _, sub := range []string{k8s.AppKindJob, k8s.AppKindCronJob} {
		verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindApp, SubKind: sub})
		letters := verbLetters(verbs)
		assert.Contains(t, letters, "d")
		assert.Contains(t, letters, "l")
		assert.Contains(t, letters, "e")
		assert.Contains(t, letters, "K")
		assert.NotContains(t, letters, "S", "%s must not expose Scale", sub)
		assert.NotContains(t, letters, "R", "%s must not expose Restart", sub)
	}
}

func TestPaletteVerbsFor_NodeExposesCordonAndDrainNotDelete(t *testing.T) {
	// Deleting a Node from a TUI is almost always the wrong move. The
	// verb list must offer cordon (C) and drain (D) but NOT K (kill).
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindNode})
	letters := verbLetters(verbs)
	assert.Contains(t, letters, "C")
	assert.Contains(t, letters, "D")
	assert.NotContains(t, letters, "K", "Node row must not expose delete")
}

func TestPaletteVerbsFor_SecretHasReveal(t *testing.T) {
	// 'v' (reveal) is the secret-specific verb — must be present.
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindSecret})
	letters := verbLetters(verbs)
	assert.Contains(t, letters, "v", "Secret must expose reveal (v)")
}

func TestPaletteVerbsFor_PodHasShellAndForward(t *testing.T) {
	verbs := PaletteVerbsFor(PaletteEntry{Kind: k8s.KindPod})
	letters := verbLetters(verbs)
	assert.Contains(t, letters, "s", "Pod must expose shell")
	assert.Contains(t, letters, "f", "Pod must expose port-forward")
}

func TestPaletteVerbsFor_UnknownKindReturnsNil(t *testing.T) {
	got := PaletteVerbsFor(PaletteEntry{Kind: "MysteryKind"})
	assert.Nil(t, got)
}

func TestPaletteVerbsFor_LowercaseAreReadUppercaseMutate(t *testing.T) {
	// Per the keymap convention. A row that mixed cases incorrectly would
	// fire a mutation on a key the user expected to be safe.
	checkConvention := func(t *testing.T, verbs []PaletteVerb) {
		t.Helper()
		for _, v := range verbs {
			require.NotEmpty(t, v.Letter)
			lower := v.Letter[0] >= 'a' && v.Letter[0] <= 'z'
			isRead := contains([]string{"d", "l", "e", "s", "f", "v"}, v.Letter)
			isMut := contains([]string{"S", "U", "R", "E", "K", "C", "D"}, v.Letter)
			if lower {
				assert.True(t, isRead, "lowercase verb %q must be in the read set", v.Letter)
			} else {
				assert.True(t, isMut, "uppercase verb %q must be in the mutator set", v.Letter)
			}
		}
	}
	for _, sub := range []string{k8s.AppKindDeployment, k8s.AppKindStatefulSet, k8s.AppKindDaemonSet} {
		t.Run(sub, func(t *testing.T) {
			checkConvention(t, PaletteVerbsFor(PaletteEntry{Kind: k8s.KindApp, SubKind: sub}))
		})
	}
	t.Run("Pod", func(t *testing.T) {
		checkConvention(t, PaletteVerbsFor(PaletteEntry{Kind: k8s.KindPod}))
	})
	t.Run("Node", func(t *testing.T) {
		checkConvention(t, PaletteVerbsFor(PaletteEntry{Kind: k8s.KindNode}))
	})
	t.Run("Secret", func(t *testing.T) {
		checkConvention(t, PaletteVerbsFor(PaletteEntry{Kind: k8s.KindSecret}))
	})
}

func verbLetters(verbs []PaletteVerb) []string {
	out := make([]string, 0, len(verbs))
	for _, v := range verbs {
		out = append(out, v.Letter)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
