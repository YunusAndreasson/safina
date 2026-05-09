package k8s

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFormatCPU(t *testing.T) {
	assert.Equal(t, "—", FormatCPU(0))
	assert.Equal(t, "100m", FormatCPU(100))
	assert.Equal(t, "1500m", FormatCPU(1500))
	assert.Equal(t, "250m", FormatCPU(250))
	assert.Equal(t, "1m", FormatCPU(1))
}

func TestFormatCPU_Negative(t *testing.T) {
	// Negative values would be a data error, but we shouldn't crash
	s := FormatCPU(-100)
	_ = s // just verify no panic
}

func TestFormatMem(t *testing.T) {
	assert.Equal(t, "—", FormatMem(0))
	assert.Equal(t, "500B", FormatMem(500))
	assert.Equal(t, "1Ki", FormatMem(1024))
	assert.Equal(t, "1Ki", FormatMem(1025)) // rounds down
	assert.Equal(t, "1Mi", FormatMem(1048576))
	assert.Equal(t, "1Mi", FormatMem(1048577))
	assert.Equal(t, "1.5Gi", FormatMem(1610612736))
	assert.Equal(t, "128Mi", FormatMem(134217728))
}

func TestFormatMem_LargeValues(t *testing.T) {
	// 1 Ti
	assert.Equal(t, "1024.0Gi", FormatMem(1099511627776))
}

func TestFormatMem_Negative(t *testing.T) {
	// Negative bytes would be a data error, verify no panic
	s := FormatMem(-100)
	_ = s
}

func TestHumanDuration_Seconds(t *testing.T) {
	assert.Equal(t, "0s", HumanDuration(0))
	assert.Equal(t, "5s", HumanDuration(5*time.Second))
	assert.Equal(t, "59s", HumanDuration(59*time.Second))
}

func TestHumanDuration_Minutes(t *testing.T) {
	assert.Equal(t, "1m", HumanDuration(60*time.Second))
	assert.Equal(t, "5m", HumanDuration(5*time.Minute))
	assert.Equal(t, "59m", HumanDuration(59*time.Minute))
}

func TestHumanDuration_Hours(t *testing.T) {
	assert.Equal(t, "1h1m", HumanDuration(61*time.Minute))
	assert.Equal(t, "1h", HumanDuration(1*time.Hour))
	assert.Equal(t, "7h59m", HumanDuration(7*time.Hour+59*time.Minute))
	assert.Equal(t, "8h", HumanDuration(8*time.Hour))
	assert.Equal(t, "23h", HumanDuration(23*time.Hour))
}

func TestHumanDuration_Days(t *testing.T) {
	// 1 day = 24h. rem = hours % 24 = 0, so outputs "1d" (no 0h suffix)
	assert.Equal(t, "1d", HumanDuration(24*time.Hour))
	// 2d12h = 60h. rem = 60 % 24 = 12, days < 3, so "2d12h"
	assert.Equal(t, "2d12h", HumanDuration(60*time.Hour))
	// 3 days. rem = 72 % 24 = 0, so "3d"
	assert.Equal(t, "3d", HumanDuration(72*time.Hour))
	// 6 days. rem = 144 % 24 = 0, so "6d"
	assert.Equal(t, "6d", HumanDuration(144*time.Hour))
}

func TestHumanDuration_Weeks(t *testing.T) {
	assert.Equal(t, "1w", HumanDuration(7*24*time.Hour))
	assert.Equal(t, "2w", HumanDuration(14*24*time.Hour))
	assert.Equal(t, "51w", HumanDuration(357*24*time.Hour))
	assert.Equal(t, "52w", HumanDuration(364*24*time.Hour))
}

func TestHumanDuration_Years(t *testing.T) {
	assert.Equal(t, "1y", HumanDuration(365*24*time.Hour))
	assert.Equal(t, "2y", HumanDuration(730*24*time.Hour))
}

func TestHumanDuration_Negative(t *testing.T) {
	assert.Equal(t, "0s", HumanDuration(-1*time.Hour))
	assert.Equal(t, "0s", HumanDuration(-99999))
}

func TestHumanAge(t *testing.T) {
	assert.Equal(t, "—", HumanAge(time.Time{}))
	// 30 seconds ago should show "30s"
	thirtySecondsAgo := time.Now().Add(-30 * time.Second)
	assert.Equal(t, "30s", HumanAge(thirtySecondsAgo))
}

func TestHumanAge_FutureTime(t *testing.T) {
	// Future time: time.Since(future) is negative → clamped to 0 → "0s"
	future := time.Now().Add(1 * time.Hour)
	assert.Equal(t, "0s", HumanAge(future))
}

// ── Property-based: HumanDuration never panics ────────────────────────

func TestHumanDuration_NeverPanics(t *testing.T) {
	// Test a range of durations including edges
	durations := []time.Duration{
		0, 1, time.Second, time.Minute, time.Hour,
		24 * time.Hour, 7 * 24 * time.Hour,
		365 * 24 * time.Hour, 10 * 365 * 24 * time.Hour,
		math.MaxInt64, math.MaxInt64 - 1,
		-1, math.MinInt64,
	}
	for _, d := range durations {
		s := HumanDuration(d)
		assert.NotEmpty(t, s, "HumanDuration(%v) returned empty string", d)
	}
}

func TestHumanDuration_Monotonic(t *testing.T) {
	// Longer durations should not produce shorter-looking strings in weird ways.
	// At minimum, the output should be different for very different inputs.
	s1 := HumanDuration(1 * time.Hour)
	s2 := HumanDuration(48 * time.Hour)
	assert.NotEqual(t, s1, s2, "1h vs 48h should produce different output")
}
