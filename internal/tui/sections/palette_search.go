package sections

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
)

type NamespaceSearchMsg struct {
	Request            uint64
	Context, Namespace string
	Entries            []PaletteEntry
	Err                error
}

// SearchNamespace reuses the normal cache-aware tree loaders and indexer.
// Explicit scope keeps a search from silently listing every cluster resource.
func SearchNamespace(ctx context.Context, mgr *k8s.Manager, cluster, namespace string, path []NodeRef, request uint64) tea.Cmd {
	return func() tea.Msg {
		result := NamespaceSearchMsg{Request: request, Context: cluster, Namespace: namespace}
		client, ok := mgr.GetClient(cluster)
		if !ok {
			result.Err = errContextNotFound
			return result
		}
		searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cache := make(map[string]*Children)
		var errs []error
		for _, fold := range []string{k8s.KindWorkloadsFold, k8s.KindConfigFold, k8s.KindSecretsFold, k8s.KindNetworkFold, k8s.KindIngressFold, k8s.KindStorageFold} {
			p := append(append([]NodeRef(nil), path...), NodeRef{Kind: fold, Name: fold})
			ch, err := loadChildren(searchCtx, client, p, nil, nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", fold, err))
				continue
			}
			cache[pathKeyFor(p)] = ch
			if fold == k8s.KindWorkloadsFold {
				for _, app := range ch.Apps {
					ap := append(append([]NodeRef(nil), p...), NodeRef{Kind: k8s.KindApp, SubKind: app.Kind, Name: app.Name, Namespace: app.Namespace, UID: app.UID})
					pods, err := loadChildren(searchCtx, client, ap, nil, nil)
					if err != nil {
						errs = append(errs, err)
						continue
					}
					cache[pathKeyFor(ap)] = pods
				}
			}
		}
		result.Entries = BuildPaletteIndex(cache)
		result.Err = errors.Join(errs...)
		return result
	}
}

func (p *Palette) SetEntries(entries []PaletteEntry) {
	p.entries = entries
	p.cursor = 0
	p.recomputeMatches()
}
func (p *Palette) SetScope(scope string) { p.scope = scope }
