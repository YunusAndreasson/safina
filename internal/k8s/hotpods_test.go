package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRankHotPods_SortsCPUDescending(t *testing.T) {
	got := rankHotPods([]PodInfo{
		{Name: "low", CPU: 10},
		{Name: "high", CPU: 1000},
		{Name: "mid", CPU: 500},
	}, 10)
	require.Len(t, got, 3)
	assert.Equal(t, "high", got[0].Name)
	assert.Equal(t, "mid", got[1].Name)
	assert.Equal(t, "low", got[2].Name)
}

func TestRankHotPods_BreaksTiesByMemoryDescending(t *testing.T) {
	got := rankHotPods([]PodInfo{
		{Name: "a", CPU: 100, Memory: 64 << 20},
		{Name: "b", CPU: 100, Memory: 256 << 20}, // same CPU, more mem
		{Name: "c", CPU: 100, Memory: 32 << 20},
	}, 10)
	require.Len(t, got, 3)
	assert.Equal(t, "b", got[0].Name, "equal CPU → highest memory wins")
	assert.Equal(t, "a", got[1].Name)
	assert.Equal(t, "c", got[2].Name)
}

func TestRankHotPods_BreaksTiesByNamespaceThenName(t *testing.T) {
	// Without the namespace/name tiebreak, two near-idle pods would
	// flicker positions on every metrics refresh. This test pins that
	// expectation.
	got := rankHotPods([]PodInfo{
		{Name: "z", Namespace: "billing", CPU: 0, Memory: 0},
		{Name: "a", Namespace: "billing", CPU: 0, Memory: 0},
		{Name: "a", Namespace: "auth", CPU: 0, Memory: 0},
	}, 10)
	require.Len(t, got, 3)
	// auth/a < billing/a < billing/z lexicographically.
	assert.Equal(t, "auth", got[0].Namespace)
	assert.Equal(t, "a", got[0].Name)
	assert.Equal(t, "billing", got[1].Namespace)
	assert.Equal(t, "a", got[1].Name)
	assert.Equal(t, "billing", got[2].Namespace)
	assert.Equal(t, "z", got[2].Name)
}

func TestRankHotPods_AppliesLimitTruncation(t *testing.T) {
	got := rankHotPods([]PodInfo{
		{Name: "1", CPU: 100},
		{Name: "2", CPU: 90},
		{Name: "3", CPU: 80},
		{Name: "4", CPU: 70},
		{Name: "5", CPU: 60},
	}, 3)
	require.Len(t, got, 3)
	// Top 3 must be the loudest 3 (highest CPU).
	assert.Equal(t, "1", got[0].Name)
	assert.Equal(t, "2", got[1].Name)
	assert.Equal(t, "3", got[2].Name)
}

func TestRankHotPods_NegativeOrZeroLimitDefaultsTo20(t *testing.T) {
	pods := make([]PodInfo, 25)
	for i := range pods {
		pods[i] = PodInfo{Name: string(rune('a' + i)), CPU: int64(100 - i)}
	}
	for _, limit := range []int{0, -1, -100} {
		got := rankHotPods(append([]PodInfo(nil), pods...), limit)
		assert.Len(t, got, 20, "limit %d should default to 20", limit)
	}
}

func TestRankHotPods_EmptyInput(t *testing.T) {
	got := rankHotPods(nil, 10)
	assert.Empty(t, got)
}

func TestRankHotPods_StableUnderRepeatedSort(t *testing.T) {
	// Calling rankHotPods twice on the same data must return the same
	// order — this is what we trade for the tiebreak chain.
	input := []PodInfo{
		{Name: "x", Namespace: "ns", CPU: 50},
		{Name: "y", Namespace: "ns", CPU: 50},
		{Name: "z", Namespace: "ns", CPU: 50},
	}
	first := rankHotPods(append([]PodInfo(nil), input...), 10)
	second := rankHotPods(append([]PodInfo(nil), input...), 10)
	require.Len(t, second, len(first))
	for i := range first {
		assert.Equal(t, first[i].Name, second[i].Name, "row %d", i)
	}
}
