package metrics

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObservationsIdentityDedupAndExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var s Store
	k := Key{Cluster: "a", Kind: "Pod", Name: "api", UID: "old"}
	r := Reading{CPU: 0, State: Available, SourceAt: now}
	first := s.Add(k, r, now)
	s.Add(k, r, now.Add(5*time.Second))
	changed := r
	changed.CPULimit = 1000
	s.Add(k, changed, now.Add(10*time.Second))
	require.Len(t, first.Points, 1)
	assert.Equal(t, now, first.Points[0].At)
	assert.Equal(t, "available", first.Latest.Status(now))
	assert.Equal(t, "stale", first.Latest.Status(now.Add(61*time.Second)))
	k.UID = "new"
	second := s.Add(k, r, now)
	assert.NotSame(t, first, second)
	s.Expire(now.Add(Retention + 11*time.Second))
	assert.Empty(t, s.Series)
	assert.Zero(t, s.count)
	s.Reset()
	assert.Nil(t, s.Series)
}

func TestStoreBoundsAndProtectedSelection(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var s Store
	selected := Key{Name: "selected"}
	s.Protected = selected
	s.Add(selected, Reading{State: Available}, now)
	for i := range MaxResources + 100 {
		s.Add(Key{Name: strconv.Itoa(i)}, Reading{}, now.Add(time.Second))
	}
	assert.LessOrEqual(t, len(s.Series), MaxResources)
	assert.NotNil(t, s.Series[selected])
	// Exercise global sample bound with distinct source times and many resources.
	s.Reset()
	for i := range 70 {
		k := Key{Name: strconv.Itoa(i)}
		for j := range PerResource + 1 {
			at := now.Add(time.Duration(j) * time.Second)
			s.Add(k, Reading{SourceAt: at, State: Available}, at)
		}
		require.LessOrEqual(t, len(s.Series[k].Points), PerResource)
	}
	assert.LessOrEqual(t, s.count, MaxPoints)
	for _, v := range s.Series {
		assert.LessOrEqual(t, len(v.Points), PerResource)
	}
}

func BenchmarkStoreThousandResources(b *testing.B) {
	var s Store
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	keys := make([]Key, 1000)
	for i := range keys {
		keys[i] = Key{Name: strconv.Itoa(i)}
	}
	b.ResetTimer()
	for i := range b.N {
		at := now.Add(time.Duration(i) * 5 * time.Second)
		for _, k := range keys {
			s.Add(k, Reading{SourceAt: at, State: Available, CPU: 100}, at)
		}
		s.Expire(at)
	}
}
