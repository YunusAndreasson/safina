package bench

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Summary holds the per-cell aggregate over N iteration durations (in
// milliseconds, float64 for percentile math) and observed byte / call counts.
type Summary struct {
	N         int
	MeanMs    float64
	StddevMs  float64
	MedianMs  float64
	P95Ms     float64
	P99Ms     float64
	MaxMs     float64
	BytesMean float64
	CallsMean float64
}

// Summarize computes a Summary from a slice of per-iteration durations and
// the parallel slices of bytes / call counts captured for the same iters.
// The three slices must be the same length; bytes/calls may be nil if the
// caller doesn't have that data.
func Summarize(durMs, bytes, calls []float64) Summary {
	s := Summary{N: len(durMs)}
	if len(durMs) == 0 {
		return s
	}
	s.MeanMs = mean(durMs)
	s.StddevMs = stddev(durMs, s.MeanMs)
	s.MedianMs = percentile(durMs, 50)
	s.P95Ms = percentile(durMs, 95)
	s.P99Ms = percentile(durMs, 99)
	s.MaxMs = maxF(durMs)
	if bytes != nil {
		s.BytesMean = mean(bytes)
	}
	if calls != nil {
		s.CallsMean = mean(calls)
	}
	return s
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func stddev(xs []float64, m float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	sq := 0.0
	for _, x := range xs {
		d := x - m
		sq += d * d
	}
	return math.Sqrt(sq / float64(len(xs)-1))
}

func maxF(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// percentile returns the nearest-rank percentile of xs. Pct in [0,100].
// xs is sorted in place — callers should pass a copy if order matters.
func percentile(xs []float64, pct float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	if pct <= 0 {
		return cp[0]
	}
	if pct >= 100 {
		return cp[len(cp)-1]
	}
	rank := int(math.Ceil(pct/100.0*float64(len(cp)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(cp) {
		rank = len(cp) - 1
	}
	return cp[rank]
}

// WilcoxonSignedRank computes the two-sided Wilcoxon signed-rank test
// p-value comparing paired samples a and b (a[i] vs b[i]). Zero-difference
// pairs are dropped per the standard. Returns p in (0,1]; smaller means
// "the two distributions differ significantly."
//
// We use a normal approximation with continuity correction, which is
// accurate when the number of non-zero pairs >= ~10. The runner enforces
// N=30 by default so we comfortably stay in that regime.
//
// Returns 1.0 (no signal) when fewer than 5 non-zero pairs survive — too
// little data to reject the null.
func WilcoxonSignedRank(a, b []float64) float64 {
	if len(a) != len(b) {
		return 1.0
	}
	type pair struct {
		abs  float64
		sign int
	}
	pairs := make([]pair, 0, len(a))
	for i := range a {
		d := a[i] - b[i]
		if d == 0 {
			continue
		}
		sign := 1
		if d < 0 {
			sign = -1
			d = -d
		}
		pairs = append(pairs, pair{abs: d, sign: sign})
	}
	n := len(pairs)
	if n < 5 {
		return 1.0
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].abs < pairs[j].abs })

	// Rank with average-rank tie correction. Also accumulate the tie-correction
	// term needed for the variance under ties: sum over tie groups of t^3 - t.
	ranks := make([]float64, n)
	tieCorrection := 0.0
	i := 0
	for i < n {
		j := i
		for j+1 < n && pairs[j+1].abs == pairs[i].abs {
			j++
		}
		avg := float64(i+j+2) / 2.0 // ranks are 1-indexed, average of (i+1)..(j+1)
		for k := i; k <= j; k++ {
			ranks[k] = avg
		}
		t := float64(j - i + 1)
		if t > 1 {
			tieCorrection += t*t*t - t
		}
		i = j + 1
	}

	wPlus := 0.0
	for k, p := range pairs {
		if p.sign > 0 {
			wPlus += ranks[k]
		}
	}
	nf := float64(n)
	mean := nf * (nf + 1) / 4.0
	variance := nf*(nf+1)*(2*nf+1)/24.0 - tieCorrection/48.0
	if variance <= 0 {
		return 1.0
	}
	// Continuity correction toward the mean.
	z := (wPlus - mean)
	if z > 0 {
		z -= 0.5
	} else if z < 0 {
		z += 0.5
	}
	z /= math.Sqrt(variance)
	// Two-sided p-value from the standard normal CDF.
	return 2 * (1 - normalCDF(math.Abs(z)))
}

// normalCDF is the cumulative distribution function for the standard normal.
// math.Erfc-based to avoid pulling in a stats library.
func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// RegressionVerdict compares a candidate Summary against a baseline at the
// configured percentile (95 by default) and returns true if the candidate is
// worse by more than tolerancePct (e.g. 15.0 for the default 15% gate).
//
// Used by `safina-bench --quick` to gate CI runs; tolerancePct comes from the
// committed bench/baseline.json or a CLI flag.
func RegressionVerdict(baseline, candidate Summary, tolerancePct float64) (regressed bool, deltaPct float64) {
	if baseline.P95Ms == 0 {
		return false, 0
	}
	deltaPct = (candidate.P95Ms - baseline.P95Ms) / baseline.P95Ms * 100.0
	return deltaPct > tolerancePct, deltaPct
}

// Percentile is the exported nearest-rank percentile, pct in [0,100]. xs is
// not modified.
func Percentile(xs []float64, pct float64) float64 { return percentile(xs, pct) }

// RatioCI is median(a)/median(b) with a percentile-bootstrap confidence
// interval. Samples are resampled independently (the two tools are separate
// processes, so samples are unpaired).
type RatioCI struct {
	Ratio, Lo, Hi float64
}

// Excludes1 reports whether the interval lies entirely on one side of 1, the
// condition for claiming one side is faster.
func (r RatioCI) Excludes1() bool { return r.Hi < 1 || r.Lo > 1 }

// MedianRatioCI bootstraps the ratio of medians with iters resamples at the
// given confidence (e.g. 0.95). The rng is seeded so a report is
// reproducible from its samples. Returns zero when either side is empty or
// b's median is zero.
func MedianRatioCI(a, b []float64, iters int, conf float64, seed uint64) RatioCI {
	if len(a) == 0 || len(b) == 0 {
		return RatioCI{}
	}
	mb := percentile(b, 50)
	if mb == 0 {
		return RatioCI{}
	}
	out := RatioCI{Ratio: percentile(a, 50) / mb}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) // #nosec G404 -- Seeded so a report reproduces from its samples.
	ratios := make([]float64, 0, iters)
	ra, rb := make([]float64, len(a)), make([]float64, len(b))
	for range iters {
		for i := range ra {
			ra[i] = a[rng.IntN(len(a))]
		}
		for i := range rb {
			rb[i] = b[rng.IntN(len(b))]
		}
		if m := percentile(rb, 50); m > 0 {
			ratios = append(ratios, percentile(ra, 50)/m)
		}
	}
	tail := (1 - conf) / 2 * 100
	out.Lo = percentile(ratios, tail)
	out.Hi = percentile(ratios, 100-tail)
	return out
}
