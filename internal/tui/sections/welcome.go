package sections

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yunus/safina/internal/tui/theme"
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

// WelcomeSail is the cluster picker's quiet brand composition. It is static:
// no timers, terminal graphics protocols, or external assets are needed.
func WelcomeSail(t *theme.Theme, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	// Reserve the wordmark and identity before
	// choosing the illustration. Tiny panes retain the name without clipping.
	art, artW := "", 0
	switch {
	case w >= 48 && h >= 28:
		art, artW = sail40, 40
	case w >= 34 && h >= 22:
		art, artW = sail28, 28
	case w >= 22 && h >= 16:
		art, artW = sail16, 16
	}
	center := func(s string) string { return lipgloss.PlaceHorizontal(w, lipgloss.Center, s) }
	lines := make([]string, 0, h)
	if art != "" {
		rows := strings.Split(art, "\n")
		// Perceptual interpolation avoids muddy RGB midpoints. Cyan catches the
		// upper sail; the foot recedes through blue into the pane background.
		colors := lipgloss.Blend1D(len(rows), t.P.Cyan, t.P.Blue, t.P.Dim)
		// The sail's visual mass is to the right of its bounding-box midpoint.
		// Shift the art left slightly; align the type on the pane's true centre.
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
	const tagline = "The Kubernetes TUI for teams that ship."
	if w >= 22 && h-len(lines) >= 4 {
		lines = append(lines, "")
		if w >= len(tagline)+4 {
			lines = append(lines, center(t.DimTxt.Render(tagline)))
		} else {
			lines = append(lines, center(t.DimTxt.Render("The Kubernetes TUI")), center(t.DimTxt.Render("for teams that ship.")))
		}
	}
	// A slightly raised optical centre leaves breathing room below the mark.
	top := max(0, (h-len(lines))*2/5)
	content := strings.Repeat("\n", top) + strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}
