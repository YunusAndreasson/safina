package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
)

// EmptyArt is an original nautical textmode vignette, drawn with the sail's
// printable-ASCII density ramp. Opt in only for settled, spacious empty states.
// It has no timers, font dependencies, terminal control sequences or assets to load.
type EmptyArt uint8

const (
	NoEmptyArt EmptyArt = iota
	BeaconArt
	AnchorArt
)

const beaconLarge = `                    .
                   .:.
                  .+%+.
               .+%%%###+.
        . . :--+%%%%####+--: . .
 . . : :--==++**%%+...+##**++==--: : . .
         ' ' :--+##+++##+--: ' '
                 :#####:
                 :##+##:
                 +##.##+
                .###.###.
                :###.###:
             .-=*#######*=-.
       ..:-=++***#######***++=-:..
. .:--==++*******#######*******++==--:. .
   ''::--===+++++++++++++++++===--::''
             ''':::::::::'''`

const beaconSmall = `          .
         +%+
       .+%##+.
. :--=+*%+.+#*+=--: .
   ':-+%%%###+-:'
       :%%.##:
       +##.##+
     .+#######+.
..:-=+***###***+=-:..
   ''::--===--::''`

const anchorLarge = `       .:===:.
    .+%%%%####+.
     +%%+:.:+##+
     :%%%=:=###:
      '-+%##+-'
         ###
 .:=====+###+=====:.
  '-====+###+====-'
         ###
 .       ###       .
+#:.     ###     .:#+
####+.   ###   .+####
:#####+. ### .+#####:
 '+#####*###*#####+'
   ':###########:'
      '-+###+-'
         ':'`

const anchorSmall = `      .:===:.
      %%+.+##
      '+%##+'
   .:==+%##+==:.
        %##
 .      ###      .
+#+.    ###    .+#+
:###*:. ### .:*###:
  '+###########+'
     '-+###+-'`

// IllustratedEmpty gives the message and action priority over decoration.
// Small panes retain text; artwork never gets cropped or hides the next step.
func IllustratedEmpty(t *theme.Theme, w, h int, motif EmptyArt, caption, hint string) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	text := emptyText(t, w, caption, hint)
	textH := lipgloss.Height(text)
	large, small := "", ""
	switch motif {
	case NoEmptyArt:
		return boundedEmptyText(t, w, h, caption, hint)
	case BeaconArt:
		large, small = beaconLarge, beaconSmall
	case AnchorArt:
		large, small = anchorLarge, anchorSmall
	}
	art := ""
	// A margin around each composition keeps it away from the pane border.
	for _, candidate := range []string{large, small} {
		if candidate != "" && textw.Width(candidate)+6 <= w && lipgloss.Height(candidate)+textH+4 <= h {
			art = candidate
			break
		}
	}
	if art == "" {
		return boundedEmptyText(t, w, h, caption, hint)
	}
	rows := strings.Split(art, "\n")
	colors := lipgloss.Blend1D(len(rows), t.P.FGFade, t.P.Blue, t.P.Dim)
	artW := textw.Width(art)
	for i, row := range rows {
		rows[i] = lipgloss.NewStyle().Foreground(colors[i]).Render(row + strings.Repeat(" ", artW-textw.StringWidth(row)))
	}
	block := textw.JoinVertical(lipgloss.Center, strings.Join(rows, "\n"), "", text)
	// Lift the composition slightly above the mathematical centre, as on welcome.
	top := max(0, (h-lipgloss.Height(block))*2/5)
	return lipgloss.NewStyle().Width(w).Height(h).Render(strings.Repeat("\n", top) + lipgloss.PlaceHorizontal(w, lipgloss.Center, block))
}

func emptyText(t *theme.Theme, w int, caption, hint string) string {
	text := t.NameDim.Render(ansi.Wrap(caption, max(1, w-2), ""))
	if hint != "" {
		text += "\n" + t.Hint.Render(ansi.Wrap(hint, max(1, w-2), ""))
	}
	return lipgloss.NewStyle().Width(min(w, textw.Width(text))).Align(lipgloss.Center).Render(text)
}

func boundedEmptyText(t *theme.Theme, w, h int, caption, hint string) string {
	rows := strings.Split(emptyText(t, w, caption, hint), "\n")
	if len(rows) > h {
		rows = rows[:h]
		rows[h-1] = ansi.Truncate(rows[h-1], max(0, w-1), "") + "…"
	}
	return lipgloss.NewStyle().Width(w).Height(h).Align(lipgloss.Center, lipgloss.Center).Render(strings.Join(rows, "\n"))
}
