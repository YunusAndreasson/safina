package k8s

import (
	"fmt"
	"time"
)

// HumanAge returns a compact, kubectl-style relative duration:
//
//	<1m   → "10s"
//	<1h   → "5m"
//	<1d   → "3h"  or  "3h45m" if the minutes matter
//	<1w   → "5d"  or  "5d6h" for the first day
//	≥1w   → "3w"
//	≥1y   → "2y"
//
// Goal: always 2–4 characters wide so it fits in a tight numeric column.
func HumanAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return HumanDuration(time.Since(t))
}

// HumanDuration formats a positive duration in compact kubectl style.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 8 {
		// Show minutes for short hour ranges so "1h59m" reads precisely.
		rem := minutes % 60
		if rem == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, rem)
	}
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	if days < 7 {
		rem := hours % 24
		if rem == 0 || days >= 3 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, rem)
	}
	weeks := days / 7
	// Stay in weeks until we reach a full year. The boundary matters: at
	// exactly 364 days the integer division `days/365` is 0, which would
	// otherwise emit a confusing "0y".
	if days < 365 {
		return fmt.Sprintf("%dw", weeks)
	}
	years := days / 365
	return fmt.Sprintf("%dy", years)
}
