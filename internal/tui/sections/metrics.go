package sections

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/metrics"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

var (
	MetricWindows = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}
	MetricActions = []string{"scale", "image", "restart", "restart_pod", "rollback", "delete", "edit", "cordon", "uncordon", "drain", "evict", "investigation_selected", "investigation_outcome", "investigation_interrupted"}
)

type MetricAction struct {
	At     time.Time
	Record history.Record
	Phases []string
}
type Metrics struct {
	Series       *metrics.Series
	Ref          NodeRef
	Context      string
	Preset       int
	Initialized  bool
	Frozen       time.Time
	Memory       bool
	ShowActivity bool
	Actions      []MetricAction
	Warnings     []string
	HistoryState string
	Cursor       int
	Generation   uint64
	ceiling      [2]int64
	cache        string
	cacheKey     string
	cacheTheme   *theme.Theme
}

func MetricsSupported(ref NodeRef) bool {
	return ref.Kind == k8s.KindApp || ref.Kind == k8s.KindPod || ref.Kind == k8s.KindNode
}

func (m *Metrics) Select(context string, ref NodeRef, series *metrics.Series) bool {
	if !m.Initialized {
		m.Preset = 1
		m.Initialized = true
	}
	changed := m.Context != context || m.Ref.Kind != ref.Kind || m.Ref.SubKind != ref.SubKind || m.Ref.Namespace != ref.Namespace || m.Ref.Name != ref.Name || m.Ref.UID != ref.UID
	m.Series = series
	if changed {
		m.Context = context
		m.Ref = ref
		m.Frozen = time.Time{}
		m.Actions = nil
		m.Warnings = nil
		m.HistoryState = ""
		m.Cursor = 0
		m.ceiling = [2]int64{}
		m.Generation++
		m.cacheKey = ""
	}
	return changed
}

func (m *Metrics) Range(now time.Time) (time.Time, time.Time) {
	end := now
	if !m.Frozen.IsZero() {
		end = m.Frozen
	}
	return end.Add(-MetricWindows[m.Preset]), end
}

func (m *Metrics) Key(key string, now time.Time) (reload, leave bool) {
	switch key {
	case "esc", "q":
		return false, true
	case "[":
		m.Preset = max(0, m.Preset-1)
		m.ceiling = [2]int64{}
		reload = true
	case "]":
		m.Preset = min(len(MetricWindows)-1, m.Preset+1)
		m.ceiling = [2]int64{}
		reload = true
	case "space", " ":
		if m.Frozen.IsZero() {
			m.Frozen = now
		} else {
			m.Frozen = time.Time{}
		}
		reload = true
	case "t":
		m.Memory = !m.Memory
	case "a":
		m.ShowActivity = !m.ShowActivity
		reload = true
	case "up", "k":
		m.Cursor = max(0, m.Cursor-1)
	case "down", "j":
		m.Cursor = min(max(0, len(m.Actions)-1), m.Cursor+1)
	}
	if reload {
		m.Generation++
	}
	m.cacheKey = ""
	return reload, false
}

func (m *Metrics) ApplyHistory(result history.Result, err error) {
	m.Actions = nil
	m.Warnings = result.Warnings
	m.HistoryState = "Safina-recorded changes; external changes not included"
	if err != nil {
		m.HistoryState = "Action history unavailable"
		return
	}
	if result.Omitted > 0 {
		m.Warnings = append(m.Warnings, fmt.Sprintf("%d earlier records omitted", result.Omitted))
	}
	groups := map[string]int{}
	for _, r := range result.Records {
		if m.Ref.UID == "" || (r.Target.UID != m.Ref.UID && r.Target.WorkloadUID != m.Ref.UID) {
			continue
		}
		switch r.Outcome {
		case "requested", "accepted", "completed", "failed", "unknown", "cancelled", "timeout", "interrupted", "recorded", "outcome", "intent", "superseded":
		default:
			continue
		}
		id := r.Operation
		if id == "" {
			id = r.ID
		}
		index, ok := groups[id]
		if !ok {
			index = len(m.Actions)
			groups[id] = index
			m.Actions = append(m.Actions, MetricAction{At: r.Time})
		}
		a := &m.Actions[index]
		a.Record = r
		a.Phases = append(a.Phases, r.Time.UTC().Format("15:04:05")+" "+r.Outcome)
	}
	m.Cursor = min(m.Cursor, max(0, len(m.Actions)-1))
	m.cacheKey = ""
}

func (m *Metrics) SelectedAction() *history.Record {
	if !m.ShowActivity || len(m.Actions) == 0 {
		return nil
	}
	r := m.Actions[m.Cursor].Record
	return &r
}

func (m *Metrics) Title() string {
	if !MetricsSupported(m.Ref) {
		return "Metrics"
	}
	kind := m.Ref.Kind
	if m.Ref.SubKind != "" {
		kind = m.Ref.SubKind
	}
	return fmt.Sprintf("Metrics · %s/%s", kind, m.Ref.Name)
}

func metricStats(points []metrics.Point, memory bool) (int64, time.Time, int) {
	var peak int64
	var at time.Time
	valid := 0
	for _, p := range points {
		if p.Valid() && !p.Stale(p.At) {
			valid++
			v := components.MetricMagnitude(p, memory)
			if at.IsZero() || v > peak {
				peak = v
				at = p.At
			}
		}
	}
	return peak, at, valid
}

func (m *Metrics) Summary(now time.Time) string {
	start, end := m.Range(now)
	b := strings.Builder{}
	fmt.Fprintf(&b, "%s · %s/%s · UID %s\nLocally observed %s — %s UTC\n", m.Title(), m.Context, m.Ref.Namespace, m.Ref.UID, start.UTC().Format("2006-01-02 15:04:05"), end.UTC().Format("2006-01-02 15:04:05"))
	if m.Series == nil {
		b.WriteString("Waiting for metrics; no earlier observations.\n")
	} else {
		points := m.Series.Window(start, end)
		latest := m.Series.Latest
		if !m.Frozen.IsZero() {
			if len(points) > 0 {
				latest = points[len(points)-1]
			} else {
				latest = metrics.Point{}
			}
		}
		fmt.Fprintf(&b, "%s; coverage %d/%d; source %s; sample window %s\n", latest.Status(end), latest.Covered, latest.Expected, latest.SourceAt.UTC().Format(time.RFC3339), latest.Window)
		for _, mem := range []bool{false, true} {
			label := "CPU"
			if mem {
				label = "Memory"
			}
			peak, at, n := metricStats(points, mem)
			cur := "—"
			if latest.Status(end) == metrics.Available {
				cur = components.MetricValue(components.MetricMagnitude(latest, mem), mem)
			}
			req, lim := components.MetricBudget(latest, mem)
			peakText := "—"
			if n > 0 {
				peakText = components.MetricValue(peak, mem) + " at " + at.UTC().Format("15:04:05")
			}
			fmt.Fprintf(&b, "%s: current %s; peak %s; %d samples\n", label, cur, peakText, n)
			if req > 0 {
				fmt.Fprintf(&b, "  request %s", components.MetricValue(req, mem))
			}
			if lim > 0 {
				label = "limit"
				if m.Ref.Kind == k8s.KindNode {
					label = "allocatable"
				}
				fmt.Fprintf(&b, "  %s %s", label, components.MetricValue(lim, mem))
			}
			b.WriteByte('\n')
		}
		b.WriteString("Gaps are unobserved, unavailable, partial, or stale intervals.\n")
	}
	if m.ShowActivity {
		b.WriteString("Related changes from Logbook\n")
		b.WriteString(m.HistoryState + "\n")
		for _, a := range m.Actions {
			fmt.Fprintf(&b, "%s %s [%s] %s\n", a.At.UTC().Format(time.RFC3339), metricActionLabel(a.Record), a.Record.ID, strings.Join(a.Phases, " → "))
		}
		for _, w := range m.Warnings {
			fmt.Fprintf(&b, "Warning: %s\n", w)
		}
	}
	return b.String()
}

func (m *Metrics) Body(t *theme.Theme, w, h int, now time.Time) string {
	if w < 1 || h < 1 {
		return ""
	}
	if !MetricsSupported(m.Ref) {
		return IdlePlaceholder(t, w, h, "Select a workload, pod, or node to see metrics.")
	}
	now = now.Truncate(time.Second)
	rev := uint64(0)
	if m.Series != nil {
		rev = m.Series.Revision
	}
	key := fmt.Sprintf("%d/%d/%d/%d/%d/%t/%d/%d/%d", w, h, rev, now.Unix(), m.Preset, m.Memory, m.Frozen.Unix(), m.Cursor, m.Generation)
	if key == m.cacheKey && t == m.cacheTheme {
		return m.cache
	}
	start, end := m.Range(now)
	// The window, clock and live/frozen state sit on the tab rule
	// (Indicators) and the keys in the footer (Hints): every row here is
	// the reading itself.
	lines := []string{}
	if w < 40 || h < 9 || (m.ShowActivity && h < 13) {
		lines = append(lines, strings.Split(m.Summary(now), "\n")...)
		return fitMetricBody(lines, w, h)
	}
	points := []metrics.Point(nil)
	latest := metrics.Point{}
	if m.Series != nil {
		points = m.Series.Window(start, end)
		latest = m.Series.Latest
		if !m.Frozen.IsZero() {
			latest = metrics.Point{}
			if len(points) > 0 {
				latest = points[len(points)-1]
			}
		}
	}
	status := latest.Status(end)
	if latest.Expected > 0 {
		status += fmt.Sprintf(" · %d/%d reported", latest.Covered, latest.Expected)
	}
	if !latest.SourceAt.IsZero() {
		status += " · source " + k8s.HumanDuration(max(time.Duration(0), end.Sub(latest.SourceAt))) + " old · " + latest.Window.String() + " sample"
	}
	lines = append(lines, t.Hint.Render(components.TruncPad(status, w)))
	both := h >= 24
	activityRows := 0
	if m.ShowActivity {
		activityRows = 5
		if len(m.Warnings) > 0 {
			activityRows++
		}
	}
	chartH := max(4, h-3-activityRows)
	if both {
		chartH = max(4, (h-4-activityRows)/2)
	}
	for _, mem := range []bool{false, true} {
		if !both && mem != m.Memory {
			continue
		}
		idx := 0
		label := "CPU"
		ink := t.P.Cyan
		if mem {
			idx = 1
			label = "Memory"
			ink = t.P.Mauve
		}
		peak, at, n := metricStats(points, mem)
		cur := "—"
		if latest.Status(end) == metrics.Available {
			cur = components.MetricValue(components.MetricMagnitude(latest, mem), mem)
		}
		text := label + " " + cur
		if n > 0 {
			text += " · peak " + components.MetricValue(peak, mem) + " @ " + at.UTC().Format("15:04:05")
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(ink).Render(components.TruncPad(text, w)))
		m.ceiling[idx] = max(m.ceiling[idx], components.MetricCeiling(points, mem))
		if n < 2 {
			lines = append(lines, lipgloss.NewStyle().Width(w).Height(chartH).Render(t.Hint.Render("Waiting for distinct samples; gaps are not zero usage.")))
		} else {
			lines = append(lines, components.RenderMetricChart(t, points, start, end, w, chartH, mem, true, latest.Status(end) != metrics.Available, true, m.ceiling[idx], lipgloss.NewStyle()))
		}
	}
	reference := t.Hint.Render("┄ request (reservation) · ") + lipgloss.NewStyle().Foreground(t.P.Yellow).Render("┄ limit (when known)")
	if m.Ref.Kind == k8s.KindNode {
		reference = t.Hint.Render("┄ request (reservation) · ") + lipgloss.NewStyle().Foreground(t.P.Yellow).Render("┄ allocatable")
	}
	lines = append(lines, components.TruncPad(reference, w))
	if m.ShowActivity {
		lines = append(lines, t.NameStrong.Render("Related changes")+t.Hint.Render(" · Logbook"))
		strip := []rune(strings.Repeat("·", w))
		idx := 0
		if !both && m.Memory {
			idx = 1
		}
		offset, plotW := components.MetricPlotColumns(w, chartH, m.ceiling[idx], idx == 1)
		for i := range offset {
			strip[i] = ' '
		}
		for i, a := range m.Actions {
			if a.At.Before(start) || a.At.After(end) {
				continue
			}
			x := int(float64(a.At.Sub(start))/float64(end.Sub(start))*float64(plotW-1)) + offset
			strip[min(w-1, x)] = '│'
			if i == m.Cursor {
				strip[min(w-1, x)] = '▼'
			}
		}
		lines = append(lines, t.DimTxt.Render(string(strip)), t.Hint.Render(components.TruncPad(m.HistoryState, w)))
		if len(m.Actions) > 0 {
			a := m.Actions[m.Cursor]
			lines = append(lines, t.Selected.Render(components.TruncPad(fmt.Sprintf("%d/%d ", m.Cursor+1, len(m.Actions))+a.At.UTC().Format("15:04:05")+" "+metricActionLabel(a.Record)+" · "+a.Record.Outcome, w)))
		} else {
			lines = append(lines, t.Hint.Render("No recorded changes in this interval."))
		}
		if len(m.Warnings) > 0 {
			lines = append(lines, t.Hint.Render(components.TruncPad(m.Warnings[0], w)))
		}
		if len(m.Actions) > 0 {
			lines = append(lines, components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↑↓", Desc: "select change"}, {Key: "↵", Desc: "Logbook entry"}}, w, components.KeyDesc{}))
		}
	}
	m.cache = fitMetricBody(lines, w, h)
	m.cacheKey = key
	m.cacheTheme = t
	return m.cache
}

// Indicators is the chart's window and clock for the right end of the tab
// rule: how far back it reaches, when it ends, and whether it is live.
func (m *Metrics) Indicators(now time.Time) []components.Indicator {
	_, end := m.Range(now.Truncate(time.Second))
	state := "live"
	if !m.Frozen.IsZero() {
		state = "frozen"
	}
	return []components.Indicator{
		{Text: fmt.Sprintf("%s locally observed", MetricWindows[m.Preset])},
		{Text: end.UTC().Format("15:04:05") + " UTC"},
		{Text: state},
	}
}

// Hints lists the chart's keys for the footer.
func (m *Metrics) Hints() []components.KeyDesc {
	activity := "show changes"
	if m.ShowActivity {
		activity = "hide changes"
	}
	return []components.KeyDesc{
		{Key: "[ ]", Desc: "range"},
		{Key: "space", Desc: "freeze/live"},
		{Key: "a", Desc: activity},
		{Key: "t", Desc: "CPU/mem"},
		{Key: "c", Desc: "copy"},
	}
}

func fitMetricBody(parts []string, w, h int) string {
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = components.TruncPad(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

func metricActionLabel(r history.Record) string {
	if strings.HasPrefix(r.Action, "investigation_") && r.Parameters["action"] != "" {
		return r.Parameters["action"]
	}
	return r.Action
}
