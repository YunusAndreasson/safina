package k8s

import (
	"testing"
	"time"
)

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"negative_clamps_to_zero", -5 * time.Second, "0s"},
		{"sub_second", 500 * time.Millisecond, "0s"},
		{"one_second", 1 * time.Second, "1s"},
		{"fifty_nine_seconds", 59 * time.Second, "59s"},
		{"one_minute", 60 * time.Second, "1m"},
		{"fifty_nine_minutes", 59 * time.Minute, "59m"},
		{"one_hour_zero_min", 1 * time.Hour, "1h"},
		{"one_hour_one_min", time.Hour + time.Minute, "1h1m"},
		{"seven_hours_fifty_nine_min", 7*time.Hour + 59*time.Minute, "7h59m"},
		{"eight_hours_drops_minutes", 8 * time.Hour, "8h"},
		{"twenty_three_hours_fifty_nine", 23*time.Hour + 59*time.Minute, "23h"},
		{"one_day_exact", 24 * time.Hour, "1d"},
		{"one_day_six_hours", 24*time.Hour + 6*time.Hour, "1d6h"},
		{"two_days_six_hours", 2*24*time.Hour + 6*time.Hour, "2d6h"},
		{"three_days_six_hours_drops", 3*24*time.Hour + 6*time.Hour, "3d"},
		{"six_days_twentythree_hours", 6*24*time.Hour + 23*time.Hour, "6d"},
		{"one_week_exact", 7 * 24 * time.Hour, "1w"},
		{"fifty_one_weeks", 51 * 7 * 24 * time.Hour, "51w"},
		{"three_hundred_sixty_three_days", 363 * 24 * time.Hour, "51w"},
		// 364d is the gap between 52 weeks (364d) and 1 year (365d).
		// Either "52w" or "1y" is reasonable, but "0y" is broken.
		{"three_hundred_sixty_four_days", 364 * 24 * time.Hour, "52w"},
		{"one_year_exact", 365 * 24 * time.Hour, "1y"},
		{"two_years", 2 * 365 * 24 * time.Hour, "2y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HumanDuration(tc.d)
			if got != tc.want {
				t.Errorf("HumanDuration(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestHumanAge_ZeroAndFuture(t *testing.T) {
	if got := HumanAge(time.Time{}); got != "—" {
		t.Errorf("HumanAge(zero) = %q, want %q", got, "—")
	}
	// Future timestamps clamp to "0s" rather than printing nonsense like
	// "negative" — what we actually want is irrelevant for safety, but the
	// function must not panic and must return a short string.
	got := HumanAge(time.Now().Add(1 * time.Hour))
	if got == "" || len(got) > 6 {
		t.Errorf("HumanAge(future) = %q, want short non-empty", got)
	}
}
