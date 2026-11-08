package sections

import (
	"sort"

	"github.com/YunusAndreasson/safina/internal/tui/components"
)

// filterTreeRows sorts lightweight indices, then copies only matching rows.
// Sorting before fuzzy ranking preserves the existing tie order. Keep the same
// sort.Slice algorithm as the full-row sorts, including its handling of equal
// keys; namespaces may contain identically named resources.
func filterTreeRows[T any](rows []T, query string, name func(*T) string, less func(*T, *T) bool) []T {
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	if less != nil {
		sort.Slice(order, func(i, j int) bool { return less(&rows[order[i]], &rows[order[j]]) })
	}
	matches := components.FuzzyMatchIter(query, func(yield func(string) bool) {
		for _, i := range order {
			if !yield(name(&rows[i])) {
				return
			}
		}
	})
	out := make([]T, len(matches))
	for i, match := range matches {
		out[i] = rows[order[match]]
	}
	return out
}
