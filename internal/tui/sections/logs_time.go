package sections

import (
	"strings"
	"time"
)

// Compact only the Kubernetes RFC3339 prefix. Raw storage, copy and search
// retain the original precision. UTC and a date heading keep times unambiguous.
func compactLogTimestamp(line string) (display, day string) {
	prefix, rest, ok := strings.Cut(line, " ")
	if !ok {
		return line, ""
	}
	stamp, err := time.Parse(time.RFC3339Nano, prefix)
	if err != nil {
		return line, ""
	}
	stamp = stamp.UTC()
	return stamp.Format("15:04:05") + " " + rest, stamp.Format("02 Jan 2006")
}
