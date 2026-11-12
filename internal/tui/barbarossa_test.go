package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/stretchr/testify/require"
)

// Barbarossa mode: the keys that change the cluster and the confirmation
// each one opens are red, and nothing else wears that red.
func TestBarbarossaRedMarksWritesOnly(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m.ctx.ActiveContext = "eu-north-1"
	th := m.ctx.Theme
	r, g, b, _ := th.P.Red.RGBA()
	red := fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)

	k := keys.Default()
	for _, w := range k.Writes() {
		require.True(t, k.IsWrite(w.Help().Key), w.Help().Key)
	}
	for _, glyph := range []string{"s", "l", "d", "y", "c", "G", "?", "enter"} {
		require.False(t, k.IsWrite(glyph), glyph)
	}
	require.True(t, k.IsWrite("e"), "e edits, as in k9s, and edits are writes")

	write := components.RenderModalKeysRow(th, []components.KeyDesc{{Key: "S", Desc: "scale", Write: true}})
	read := components.RenderModalKeysRow(th, []components.KeyDesc{{Key: "l", Desc: "logs"}})
	require.Contains(t, write, red)
	require.NotContains(t, read, red)

	for name, card := range map[string]string{
		"confirm": m.renderConfirmModal("restart app", "payments-api"),
		"scale":   m.renderScaleModal(),
	} {
		require.Contains(t, card, red, name+" is a write: red frame, title and key")
		top := strings.SplitN(card, "\n", 2)[0]
		require.Contains(t, top, red, name+": the frame itself is red")
	}

	m.pfTarget.Kind, m.pfTarget.Name, m.pfTarget.Namespace = "Pod", "cart-abc12", "storefront"
	require.NotContains(t, m.renderPortForwardModal(), red, "a port-forward changes nothing: no red")
}
