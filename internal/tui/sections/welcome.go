package sections

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// These ASCII silhouettes trace site/src/assets/sail.svg. Separate optical
// sizes keep the curved foot and inward edge intact in small terminal panes.
// The two-to-one cell aspect ratio preserves the website mark's proportions.
const sail40 = `         .+*=:.
           *%%%#+-.
           .%%%%%@@*=.
            +%%%%%%%@@*-
            :%%%%%%%%%%%%=.
            :%%%%%%%%%%%%%%=.
            :%%%%%%%%%%%%%%%%=
            =%%%%%%%%%%%%%%%%%#:
            *#%%%%%%%%%%%%%%%%%%=
           -####%%%%%%%%%%%%%%%%%*
          .#######%%%%%%%%%%%%%####.
          *############%%%##########.
         =##########################*
        =*###########################+
       =****#########################*:
      +******#######################**+
    .+*********###################*****:
   :++***+==--:::::::::::::---++*******=
  -+=-:.                          :-=+*+
.:.                                    .`

const sail28 = `      .=*=:.
        *%%@*=.
        -%%%%%@#-.
         %%%%%%%%%+.
         %%%%%%%%%%%=.
        -%%%%%%%%%%%%#:
        *##%%%%%%%%%%%%=
       -#####%%%%%%%%%##+
      :##################=
     .*###################-
    :****#################*.
   -******##############***-
  =++=--::.       .::--=+**+
.::                       .:`

const sail16 = `    =*=:
     %%%%=.
     #%%%%%+.
    :#%%%%%%#:
    *###%%%%%#-
   +###########.
 .+***#++*#####=
.-:.        .:-=`

// brandLines is the sail, wordmark and tagline centred in w cells, choosing
// the largest sail that keeps the block within maxH rows. Tiny spaces keep
// the name without clipping.
func brandLines(t *theme.Theme, w, maxH int) []string {
	const tagline = "The Kubernetes TUI for teams that ship fast."
	taglineRows := 1
	if w < len(tagline)+4 {
		taglineRows = 2
	}
	text := 1 + 1 + taglineRows // name, gap, tagline
	art, artW := "", 0
	switch {
	case w >= 48 && maxH >= 20+1+text:
		art, artW = sail40, 40
	case w >= 34 && maxH >= 14+1+text:
		art, artW = sail28, 28
	case w >= 22 && maxH >= 8+1+text:
		art, artW = sail16, 16
	}
	center := func(s string) string { return lipgloss.PlaceHorizontal(w, lipgloss.Center, s) }
	var lines []string
	if art != "" {
		rows := strings.Split(art, "\n")
		// Perceptual interpolation avoids muddy RGB midpoints. Cyan catches the
		// upper sail; the foot recedes through blue into the background.
		colors := lipgloss.Blend1D(len(rows), t.P.Cyan, t.P.Blue, t.P.Dim)
		// The sail's visual mass is to the right of its bounding-box midpoint.
		// Shift the art left slightly; align the type on the true centre.
		left := max(0, (w-artW)/2-artW/22)
		for i, row := range rows {
			style := lipgloss.NewStyle().Foreground(colors[i])
			lines = append(lines, strings.Repeat(" ", left)+style.Render(row))
		}
		lines = append(lines, "")
	}
	name := "s a f i n a"
	if w < len(name) {
		name = "safina"
	}
	lines = append(lines, center(t.NameStrong.Render(ansi.Truncate(name, w, ""))))
	if w >= 22 && maxH-len(lines) >= 2 {
		lines = append(lines, "")
		if taglineRows == 1 {
			lines = append(lines, center(t.DimTxt.Render(tagline)))
		} else {
			lines = append(lines, center(t.DimTxt.Render("The Kubernetes TUI")), center(t.DimTxt.Render("for teams that ship fast.")))
		}
	}
	return lines
}

// harbourListWidth is the tagline's width: the cluster list lines up under
// it instead of stretching across a wide terminal.
const harbourListWidth = 44

// HarbourListWidth is the width the cluster list is rendered at.
func HarbourListWidth(w int) int { return min(w, harbourListWidth) }

// Harbour is the cluster picker page: the brand above the list of clusters.
// The list always fits; the sail shrinks, then drops, to make room. list is
// already rendered at HarbourListWidth(w).
func Harbour(t *theme.Theme, w, h int, list string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	listLines := strings.Split(list, "\n")
	const gap = 2
	brand := brandLines(t, w, h-len(listLines)-gap)
	if len(brand)+gap+len(listLines) > h {
		brand = nil
	}
	lines := append([]string(nil), brand...)
	if len(brand) > 0 {
		lines = append(lines, make([]string, gap)...)
	}
	left := strings.Repeat(" ", max(0, (w-HarbourListWidth(w))/2))
	for _, ln := range listLines {
		lines = append(lines, left+ln)
	}
	top := max(0, (h-len(lines))*2/5)
	content := strings.Repeat("\n", top) + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(content)
}
