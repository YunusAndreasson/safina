package tui

import (
	"time"

	"github.com/yunus/safina/internal/k8s"
)

// clusterHealthMsg carries the periodic cluster-health snapshot for the
// topbar pill. Err is set on failure; the model keeps the previous
// snapshot on a transient failure rather than zeroing the pill.
type clusterHealthMsg struct {
	Context string // the active context at the time of the load
	Health  k8s.ClusterHealth
	Err     error
}

// refreshMsg is delivered every refreshInterval seconds; the root reloads
// deployments if the main view is active.
type refreshMsg struct{}

// clockMsg fires once per second so the topbar's wall clock can re-render
// without the user pressing a key. We keep the time state on the model and
// re-tick on every receipt — the entire pump runs in one goroutine, no
// drift accumulates.
type clockMsg time.Time

// errorMsg is the catch-all error envelope for failed loaders.
type errorMsg struct{ err error }

// clearBannerMsg fires after a delay to dismiss a transient banner. The epoch
// is captured when the timer is scheduled and compared against the model's
// current bannerEpoch on receipt — older tickers race-cancel themselves so a
// freshly raised banner isn't wiped early by a previous timer that's still
// in-flight. (Setting any new banner bumps bannerEpoch.)
type clearBannerMsg struct{ epoch uint64 }
