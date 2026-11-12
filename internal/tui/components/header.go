package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// HeaderRows is the height of the full header: a row of air, the six-row
// sail with the four rows of facts and keys centred beside it, and a row of
// air between the header and the page. Below
// MinHeaderHeight rows or MinHeaderWidth columns the app keeps the one-row
// topbar instead.
const (
	HeaderRows      = 8
	infoRows        = 4
	MinHeaderHeight = 35
	MinHeaderWidth  = 100
)

// HeaderInput is what the full header shows: which cluster this is and as
// whom, how the cluster is doing, the keys that work on this page, and the
// mark.
type HeaderInput struct {
	Context, Cluster, User, ServerVersion string
	Health                                ClusterHealth
	Keys                                  []KeyDesc
	AppVersion                            string
}

// groupGap is the least space between two blocks; on a wider screen the
// blocks spread out evenly. Inside a block the gaps are smaller still, so
// the eye reads facts, then blocks.
const groupGap = 6

// HeaderMargin insets the header to the column the page starts on inside
// its frame (the side, then the gutter), so the header, the location, the
// tabs and the page share one left edge and one right.
const HeaderMargin = 2

// PageHeader renders the full header, HeaderRows tall and exactly w wide:
//
//	                                                                           =*=:
//	context  minikube      nodes  1/1 ready              l logs     s shell     %%%%=.
//	cluster  minikube      pods   19/19 running          e events   f forward  :#%%%%%%#:     s a f i n a
//	user     minikube      cpu    ━─────────   5%        d yaml     m metrics +###########.   v0.3.0
//	k8s      v1.31.0       mem    ━─────────   4%        r refresh  ? help  .+***#++*#####=
//	                                                                       .-:.        .:-=
//
// Four blocks spread evenly across the width, in reading order: which
// cluster this is (first, because every cluster is production), how it is
// doing, what can be done here, and the sail. The sail is the first to give way, then key columns, then the
// pulse; the cluster's name never does.
func PageHeader(t *theme.Theme, w int, in HeaderInput) string {
	if w <= 0 {
		return ""
	}
	identity := identityBlock(t, in)
	pulse := pulseBlock(t, in.Health)
	logo := logoBlock(t, in.AppVersion)

	// Blocks in reading order, each with the row it starts on: the facts
	// centre on the sail, rows 2–5; the sail takes rows 1–6.
	type block struct {
		rows []string
		top  int
	}
	inner := w - 2*HeaderMargin
	blocks := []block{{identity, 2}}
	used := blockWidth(identity)
	if used+groupGap+blockWidth(pulse) <= inner {
		blocks = append(blocks, block{pulse, 2})
		used += groupGap + blockWidth(pulse)
	}
	showLogo := inner-used-groupGap-blockWidth(logo)-groupGap >= 2*14
	if showLogo {
		used += groupGap + blockWidth(logo)
	}
	if keys := keysBlock(t, in.Keys, inner-used-groupGap); keys != nil {
		blocks = append(blocks, block{keys, 2})
	}
	if showLogo {
		blocks = append(blocks, block{logo, 1})
	}

	// The blocks share the width evenly, edge to edge, so a wide screen
	// spreads them out instead of leaving a hole in the middle.
	total := 0
	for _, b := range blocks {
		total += blockWidth(b.rows)
	}
	gaps := make([]int, len(blocks))
	if n := len(blocks) - 1; n > 0 {
		spare := max(n*groupGap, inner-total)
		for i := 1; i <= n; i++ {
			gaps[i] = spare / n
			if i <= spare%n {
				gaps[i]++
			}
		}
	}

	rows := make([]string, HeaderRows)
	for r := range HeaderRows {
		var line strings.Builder
		line.WriteString(strings.Repeat(" ", HeaderMargin))
		for i, b := range blocks {
			line.WriteString(strings.Repeat(" ", gaps[i]))
			bw := blockWidth(b.rows)
			if k := r - b.top; k >= 0 && k < len(b.rows) {
				line.WriteString(b.rows[k])
				line.WriteString(strings.Repeat(" ", bw-textw.Width(b.rows[k])))
			} else {
				line.WriteString(strings.Repeat(" ", bw))
			}
		}
		l := line.String()
		rows[r] = fitLine(l+strings.Repeat(" ", max(0, w-textw.Width(l))), w)
	}
	return strings.Join(rows, "\n")
}

// blockWidth is the cell width of a block's widest row.
func blockWidth(block []string) int {
	n := 0
	for _, row := range block {
		n = max(n, textw.Width(row))
	}
	return n
}

// padBlock pads every row to the block's widest, so blocks line up when
// they are joined side by side.
func padBlock(rows []string) []string {
	w := blockWidth(rows)
	out := make([]string, len(rows))
	for i := range rows {
		row := ""
		if i < len(rows) {
			row = rows[i]
		}
		out[i] = row + strings.Repeat(" ", w-textw.Width(row))
	}
	return out
}

// headerValueW caps an identity value; long cluster names truncate.
const headerValueW = 28

func identityBlock(t *theme.Theme, in HeaderInput) []string {
	label := lipgloss.NewStyle().Foreground(t.P.Dim)
	value := lipgloss.NewStyle().Foreground(t.P.FG)
	row := func(name, v string, st lipgloss.Style) string {
		if v == "" {
			v = "—"
			st = label
		}
		return label.Render(fmt.Sprintf("%-8s ", name)) + st.Render(ansi.Truncate(v, headerValueW, "…"))
	}
	return padBlock([]string{
		row("context", in.Context, lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)),
		row("cluster", in.Cluster, value),
		row("user", in.User, value),
		row("k8s", in.ServerVersion, value),
	})
}

// barCells is the width of a utilisation bar's interior.
const barCells = 10

func pulseBlock(t *theme.Theme, h ClusterHealth) []string {
	label := lipgloss.NewStyle().Foreground(t.P.Dim)
	lbl := func(s string) string { return label.Render(fmt.Sprintf("%-6s ", s)) }
	if !h.Show {
		dash := label.Render("—")
		return padBlock([]string{lbl("nodes") + dash, lbl("pods") + dash, lbl("cpu") + dash, lbl("mem") + dash})
	}
	good := lipgloss.NewStyle().Foreground(t.P.Green)
	warn := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true)
	bad := lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
	dim := lipgloss.NewStyle().Foreground(t.P.FGFade)

	nodes := good.Render(fmt.Sprintf("%d/%d", h.NodesReady, h.NodesTotal)) + dim.Render(" ready")
	if h.NodesReady < h.NodesTotal {
		nodes = warn.Render(fmt.Sprintf("%d/%d", h.NodesReady, h.NodesTotal)) + dim.Render(" ready")
	}
	if h.PressureCount > 0 {
		nodes += bad.Render(fmt.Sprintf(" · ⚠ %d pressure", h.PressureCount))
	}
	// Only the problem takes colour: the healthy count stays calm beside it.
	pods := good.Render(fmt.Sprintf("%d/%d", h.PodsRunning, h.PodsTotal)) + dim.Render(" running")
	switch {
	case h.PodsCrashing > 0:
		pods += dim.Render(" · ") + bad.Render(fmt.Sprintf("%d crashing", h.PodsCrashing))
	case h.PodsPending > 0:
		pods += dim.Render(" · ") + warn.Render(fmt.Sprintf("%d pending", h.PodsPending))
	}
	return padBlock([]string{
		lbl("nodes") + nodes,
		lbl("pods") + pods,
		lbl("cpu") + utilBar(t, h.CPUFrac),
		lbl("mem") + utilBar(t, h.MemFrac),
	})
}

// utilBar is a ten-cell gauge in the same line family as the rules: a
// heavy run in the utilisation colour over a dim track, then the
// percentage. Any use at all shows at least one cell.
func utilBar(t *theme.Theme, frac float64) string {
	frac = min(1, max(0, frac))
	n := int(frac*barCells + 0.5)
	if n == 0 && frac > 0 {
		n = 1
	}
	ink := lipgloss.NewStyle().Foreground(t.UtilColor(frac))
	track := lipgloss.NewStyle().Foreground(t.P.Dim)
	// The figure is quiet until the load is worth a look.
	figure := lipgloss.NewStyle().Foreground(t.P.FGFade)
	if frac >= 0.5 {
		figure = ink.Bold(true)
	}
	return ink.Render(strings.Repeat("━", n)) + track.Render(strings.Repeat("─", barCells-n)) + figure.Render(fmt.Sprintf(" %3d%%", int(frac*100+0.5)))
}

// keysBlock lays the page's keys out column by column, infoRows deep, as
// many columns as fit in w. nil when not even one column fits.
func keysBlock(t *theme.Theme, keys []KeyDesc, w int) []string {
	if len(keys) == 0 || w <= 0 {
		return nil
	}
	const colGap = 3
	rows := make([]string, infoRows)
	used := 0
	for start := 0; start < len(keys); start += infoRows {
		col := keys[start:min(len(keys), start+infoRows)]
		keyW, descW := 0, 0
		for _, k := range col {
			keyW = max(keyW, textw.Width(k.Key))
			descW = max(descW, textw.Width(k.Desc))
		}
		colW := keyW + 1 + descW
		gap := 0
		if used > 0 {
			gap = colGap
		}
		if used+gap+colW > w {
			break
		}
		for r := range infoRows {
			cell := strings.Repeat(" ", colW)
			if r < len(col) {
				k := col[r]
				keyStyle := t.HelpKey
				if k.Write {
					keyStyle = lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
				}
				cell = keyStyle.Render(fmt.Sprintf("%*s", keyW, k.Key)) + " " + t.HelpDesc.Render(fmt.Sprintf("%-*s", descW, k.Desc))
			}
			rows[r] += strings.Repeat(" ", gap) + cell
		}
		used += gap + colW
	}
	if used == 0 {
		return nil
	}
	return rows
}

// headerSail is the brand's sail at header size, cut from the Harbour's
// drawing: six rows, in the same cyan-to-blue ink.
var headerSail = []string{
	"    =*=:        ",
	"     %%%%=.     ",
	"    :#%%%%%%#:  ",
	"   +###########.",
	" .+***#++*#####=",
	".-:.        .:-=",
}

// logoBlock is the sail with the wordmark and version beside it, given
// room to breathe.
func logoBlock(t *theme.Theme, appVersion string) []string {
	colors := lipgloss.Blend1D(len(headerSail), t.P.Cyan, t.P.Blue, t.P.Dim)
	word := make([]string, len(headerSail))
	word[2] = lipgloss.NewStyle().Foreground(t.P.FG).Bold(true).Render("s a f i n a")
	word[3] = lipgloss.NewStyle().Foreground(t.P.Dim).Render(appVersion)
	out := make([]string, len(headerSail))
	for i := range headerSail {
		out[i] = lipgloss.NewStyle().Foreground(colors[i]).Render(headerSail[i]) + "    " + word[i]
	}
	return padBlock(out)
}
