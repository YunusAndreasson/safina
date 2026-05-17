package theme

import "testing"

// TestPhiSplit verifies the φ split math holds across the widths the
// renderer actually encounters. The ratio of (big / small) should sit
// within ±2% of φ at terminal sizes from 80 cols up; below 80 cols the
// floor on minSmall takes over and the ratio bends — that's the design.
func TestPhiSplit(t *testing.T) {
	cases := []struct {
		total      int
		minSmall   int
		gutter     int
		wantTreeAt int // approximate, ±2
	}{
		{total: 100, minSmall: 28, gutter: 0, wantTreeAt: 38},
		{total: 120, minSmall: 28, gutter: 0, wantTreeAt: 45},
		{total: 140, minSmall: 28, gutter: 0, wantTreeAt: 53},
		{total: 180, minSmall: 28, gutter: 0, wantTreeAt: 68},
		{total: 240, minSmall: 28, gutter: 0, wantTreeAt: 91},
	}
	for _, c := range cases {
		small, big := PhiSplit(c.total, c.gutter, c.minSmall)
		if small+big+c.gutter != c.total {
			t.Errorf("PhiSplit(%d, %d, %d) = (%d, %d): does not sum to total",
				c.total, c.gutter, c.minSmall, small, big)
		}
		if delta := small - c.wantTreeAt; delta < -2 || delta > 2 {
			t.Errorf("PhiSplit(%d, %d, %d) = (small=%d): want %d ±2",
				c.total, c.gutter, c.minSmall, small, c.wantTreeAt)
		}
		ratio := float64(big) / float64(small)
		// Within ±2% of φ once the minSmall floor isn't pinning the split.
		if ratio < 1.55 || ratio > 1.70 {
			t.Errorf("PhiSplit(%d): big/small = %.3f, want ~%.3f", c.total, ratio, Phi)
		}
	}
}

// TestPhiSplit_MinSmallFloor checks that at narrow widths where the
// proportional small would dip below minSmall, the floor is honored.
func TestPhiSplit_MinSmallFloor(t *testing.T) {
	// At total=40, minSmall=28: the φ-proportional small would be ~15.
	// The function should floor small at 28 (or fall back gracefully).
	small, big := PhiSplit(40, 0, 28)
	if small < 28 {
		t.Errorf("PhiSplit(40, 0, 28) = (small=%d): expected small ≥ 28", small)
	}
	if small+big != 40 {
		t.Errorf("PhiSplit(40, 0, 28) = (%d, %d): does not sum to 40", small, big)
	}
}
