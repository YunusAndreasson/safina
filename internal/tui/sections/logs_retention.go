package sections

import (
	"strings"
	"unicode/utf8"
)

const (
	maxLogLines     = 10000
	maxLogBytes     = 8 << 20
	maxLogLineBytes = 256 << 10
)

// trimLogStorage leaves headroom so a busy stream does not compact on every
// record. Clone retained strings: Split's substrings otherwise keep discarded
// portions of a large input chunk alive even after their slice entries vanish.
func (l *Logs) trimLogStorage(changedFrom int) (dropped int, changed bool) {
	for i := changedFrom; i < len(l.lines); i++ {
		if len(l.lines[i]) > maxLogLineBytes {
			cut := len(l.lines[i]) - maxLogLineBytes
			for cut < len(l.lines[i]) && !utf8.RuneStart(l.lines[i][cut]) {
				cut++
			}
			l.logBytes -= cut
			l.discardedLogBytes += int64(cut)
			l.lines[i] = strings.Clone(l.lines[i][cut:])
			changed = true
		}
	}
	if len(l.lines) <= maxLogLines && l.logBytes <= maxLogBytes {
		if changed {
			retained := make([]string, len(l.lines))
			for i, line := range l.lines {
				retained[i] = strings.Clone(line)
			}
			l.lines = retained
		}
		return 0, changed
	}
	targetLines, targetBytes := maxLogLines, maxLogBytes
	if len(l.lines) > maxLogLines {
		targetLines = maxLogLines * 9 / 10
	}
	if l.logBytes > maxLogBytes {
		targetBytes = maxLogBytes * 9 / 10
	}
	for dropped < len(l.lines)-1 && (len(l.lines)-dropped > targetLines || l.logBytes > targetBytes) {
		n := len(l.lines[dropped]) + 1
		l.logBytes -= n
		l.discardedLogBytes += int64(n)
		dropped++
	}
	l.discardedLogLines += int64(dropped)
	retained := make([]string, len(l.lines)-dropped)
	for i, line := range l.lines[dropped:] {
		retained[i] = strings.Clone(line)
	}
	l.lines = retained
	return dropped, true
}
