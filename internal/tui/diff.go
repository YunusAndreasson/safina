package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// diffMaxLines caps how many diff body lines we render in the edit-confirm
// modal. Longer diffs collapse to a "… (N more)" trailer so the modal
// stays bounded — the operator can always abort and re-open the file in
// their editor if they need to inspect the rest.
const diffMaxLines = 22

// renderUnifiedDiff produces the colored body of the edit-confirm modal:
// a unified diff trimmed to diffMaxLines, with +/- lines tinted (green /
// red) on a background hint tile, context lines dim, hunk headers (@@)
// rendered in cyan as section markers. Returns "" when the two byte
// slices are identical (caller should suppress the diff block in that
// case — but we never reach the modal for a no-op edit anyway).
//
// We use hexops/gotextdiff (myers algorithm) rather than a hand-rolled
// LCS because the operator's expected diff style is GNU unified output;
// matching the format means muscle memory from `git diff` and `kubectl
// diff` carries over.
func renderUnifiedDiff(t *theme.Theme, orig, edited []byte) string {
	if string(orig) == string(edited) {
		return ""
	}
	edits := myers.ComputeEdits(span.URIFromPath("a"), string(orig), string(edited))
	unified := fmt.Sprint(gotextdiff.ToUnified("a", "b", string(orig), edits))

	// Drop the two-line file header (---/+++) — we already show the
	// resource path in the modal title; restating it adds noise.
	lines := strings.Split(unified, "\n")
	body := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(ln, "--- ") || strings.HasPrefix(ln, "+++ ") {
			continue
		}
		if ln == "" {
			continue
		}
		body = append(body, ln)
	}

	addStyle := lipgloss.NewStyle().Foreground(t.P.Green)
	delStyle := lipgloss.NewStyle().Foreground(t.P.Red)
	hunkStyle := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)
	ctxStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)

	out := make([]string, 0, len(body))
	truncated := false
	for i, ln := range body {
		if i >= diffMaxLines {
			truncated = true
			break
		}
		switch {
		case strings.HasPrefix(ln, "@@"):
			out = append(out, hunkStyle.Render(ln))
		case strings.HasPrefix(ln, "+"):
			out = append(out, addStyle.Render(ln))
		case strings.HasPrefix(ln, "-"):
			out = append(out, delStyle.Render(ln))
		default:
			out = append(out, ctxStyle.Render(ln))
		}
	}
	if truncated {
		more := len(body) - diffMaxLines
		out = append(out, t.Hint.Render(fmt.Sprintf("… %d more line(s)", more)))
	}
	return strings.Join(out, "\n")
}
