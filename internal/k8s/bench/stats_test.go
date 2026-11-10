package bench

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPercentile_NearestRank(t *testing.T) {
	xs := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	// nearest-rank: rank = ceil(0.95 * 10) - 1 = 9 → 100
	assert.InDelta(t, 100.0, percentile(xs, 95), 0.001)
	assert.InDelta(t, 50.0, percentile(xs, 50), 0.001)
	assert.InDelta(t, 10.0, percentile(xs, 0), 0.001)
	assert.InDelta(t, 100.0, percentile(xs, 100), 0.001)
}

func TestSummarize_Empty(t *testing.T) {
	s := Summarize(nil, nil, nil)
	assert.Equal(t, 0, s.N)
	assert.InDelta(t, 0.0, s.MeanMs, 0.001)
}

func TestSummarize_BasicShape(t *testing.T) {
	dur := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	s := Summarize(dur, nil, nil)
	assert.Equal(t, 10, s.N)
	assert.InDelta(t, 5.5, s.MeanMs, 0.001)
	assert.InDelta(t, 3.0276, s.StddevMs, 0.001) // sample stddev
	assert.InDelta(t, 5.0, s.MedianMs, 0.001)
	assert.InDelta(t, 10.0, s.MaxMs, 0.001)
}

func TestWilcoxon_IdenticalDistributions(t *testing.T) {
	// Two interleaved noisy samples around the same mean — should NOT reject.
	a := []float64{10, 11, 9, 12, 10, 11, 9, 10, 11, 10, 12, 9}
	b := []float64{11, 10, 12, 9, 11, 10, 12, 11, 9, 11, 10, 12}
	p := WilcoxonSignedRank(a, b)
	assert.Greater(t, p, 0.1, "p should be high for similar distributions")
}

func TestWilcoxon_StrongImprovement(t *testing.T) {
	// b is consistently larger than a by ~5ms — clean significance.
	a := []float64{10, 11, 9, 12, 10, 11, 9, 10, 11, 10, 12, 9, 10, 11, 10}
	b := make([]float64, len(a))
	for i, v := range a {
		b[i] = v + 5
	}
	p := WilcoxonSignedRank(a, b)
	assert.Less(t, p, 0.01, "consistent +5ms shift should be highly significant")
}

func TestWilcoxon_LengthMismatch(t *testing.T) {
	p := WilcoxonSignedRank([]float64{1, 2}, []float64{1, 2, 3})
	assert.InDelta(t, 1.0, p, 0.001)
}

func TestWilcoxon_TooFewPairs(t *testing.T) {
	p := WilcoxonSignedRank([]float64{1, 2, 3, 4}, []float64{1, 2, 3, 4})
	// All-zero diffs → 0 non-zero pairs → no signal.
	assert.InDelta(t, 1.0, p, 0.001)
}

func TestRegressionVerdict(t *testing.T) {
	baseline := Summary{P95Ms: 100}
	worse := Summary{P95Ms: 120}
	better := Summary{P95Ms: 80}

	regressed, delta := RegressionVerdict(baseline, worse, 15)
	assert.True(t, regressed)
	assert.InDelta(t, 20.0, delta, 0.001)

	regressed, delta = RegressionVerdict(baseline, better, 15)
	assert.False(t, regressed)
	assert.InDelta(t, -20.0, delta, 0.001)

	// Tolerance applied: 14% within budget.
	regressed, _ = RegressionVerdict(baseline, Summary{P95Ms: 114}, 15)
	assert.False(t, regressed)
}

func TestNormalCDF_Sanity(t *testing.T) {
	// CDF(0) = 0.5, CDF(1.96) ≈ 0.975 (the canonical "5% two-sided" critical).
	assert.InDelta(t, 0.5, normalCDF(0), 0.001)
	assert.InDelta(t, 0.975, normalCDF(1.96), 0.001)
}

func TestStddev_Stable(t *testing.T) {
	xs := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	m := mean(xs)
	s := stddev(xs, m)
	assert.InDelta(t, 5.0, m, 0.001)
	assert.InDelta(t, 2.138, s, 0.001) // canonical worked example
	if math.IsNaN(s) {
		t.Fatal("stddev should not be NaN")
	}
}

func TestMedianRatioCI(t *testing.T) {
	fast := make([]float64, 200)
	slow := make([]float64, 200)
	same := make([]float64, 200)
	for i := range fast {
		jitter := float64(i%17) / 17
		fast[i] = 10 + jitter
		slow[i] = 20 + 2*jitter
		same[i] = 10 + float64((i*7)%17)/17
	}
	r := MedianRatioCI(fast, slow, 2000, 0.95, 1)
	if !r.Excludes1() || r.Hi >= 1 || r.Ratio < 0.45 || r.Ratio > 0.55 {
		t.Fatalf("fast vs slow: %+v, want ratio ≈ 0.5 with CI below 1", r)
	}
	r = MedianRatioCI(fast, same, 2000, 0.95, 1)
	if r.Excludes1() {
		t.Fatalf("identical distributions: %+v, want CI spanning 1", r)
	}
	if again := MedianRatioCI(fast, same, 2000, 0.95, 1); again != r {
		t.Fatalf("same seed gave %+v then %+v", r, again)
	}
	if (MedianRatioCI(nil, slow, 10, 0.95, 1) != RatioCI{}) {
		t.Fatal("empty side must give the zero value")
	}
}
