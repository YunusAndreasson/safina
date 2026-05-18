package bench

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleResult() *Result {
	return &Result{
		StartedAt: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		Seed:      42,
		Cells: []Cell{
			{
				Variant: V0Baseline, Scenario: S1ColdListPods,
				DurMs:        []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19},
				Bytes:        []float64{1000, 1100, 1050, 1075, 1090, 1100, 1080, 1095, 1085, 1090},
				Calls:        []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
				ContentTypes: map[string]int{"json": 10},
				Summary: Summarize(
					[]float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19},
					[]float64{1000, 1100, 1050, 1075, 1090, 1100, 1080, 1095, 1085, 1090},
					[]float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
				),
			},
			{
				Variant: V1Protobuf, Scenario: S1ColdListPods,
				DurMs:        []float64{6, 7, 7, 8, 8, 9, 9, 10, 10, 11},
				Bytes:        []float64{400, 410, 420, 410, 405, 415, 408, 412, 410, 411},
				Calls:        []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
				ContentTypes: map[string]int{"k8s-proto": 10},
				Summary: Summarize(
					[]float64{6, 7, 7, 8, 8, 9, 9, 10, 10, 11},
					[]float64{400, 410, 420, 410, 405, 415, 408, 412, 410, 411},
					[]float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
				),
			},
		},
	}
}

func TestWriteCSV_Shape(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteCSV(&buf, sampleResult()))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// header + 10 V0 rows + 10 V1 rows = 21
	assert.Len(t, lines, 21)
	assert.Equal(t, "variant,scenario,iter,duration_ms,bytes,api_calls,content_type", lines[0])
	assert.Contains(t, lines[1], string(V0Baseline))
}

func TestWriteMarkdown_RanksByP95(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteMarkdown(&buf, sampleResult(), 15.0))
	md := buf.String()
	// V1 (faster) should appear before V0 in the table because rank is p95-asc.
	v0Idx := strings.Index(md, string(V0Baseline))
	v1Idx := strings.Index(md, string(V1Protobuf))
	assert.Positive(t, v0Idx)
	assert.Positive(t, v1Idx)
	assert.Less(t, v1Idx, v0Idx, "V1 should rank above V0 on p95")
	assert.Contains(t, md, "improvement", "V1's huge delta should flag as improvement")
}

func TestBaseline_RoundTrip(t *testing.T) {
	res := sampleResult()
	b := NewBaseline(res)

	var buf bytes.Buffer
	require.NoError(t, WriteBaseline(&buf, b))

	got, err := ReadBaseline(&buf)
	require.NoError(t, err)
	assert.Equal(t, b.Seed, got.Seed)
	v, ok := got.Lookup(V0Baseline, S1ColdListPods)
	assert.True(t, ok)
	assert.InDelta(t, b.Cells[string(V0Baseline)+"|"+string(S1ColdListPods)], v, 0.001)
}

func TestReadBaseline_Empty(t *testing.T) {
	got, err := ReadBaseline(strings.NewReader(""))
	require.NoError(t, err)
	assert.NotNil(t, got.Cells)
	assert.Empty(t, got.Cells)
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512 B", humanBytes(512))
	assert.Equal(t, "1.0 KiB", humanBytes(1024))
	assert.Equal(t, "1.5 MiB", humanBytes(1024*1024*1.5))
}
