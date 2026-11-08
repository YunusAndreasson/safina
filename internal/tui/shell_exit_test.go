package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestShellFailureRemainsVisibleUntilDismissed(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  sections.ShellExitMsg
		want string
	}{
		{"startup", sections.ShellExitMsg{Err: errors.New("command terminated with exit code 127")}, "exit code 127"},
		{"forbidden", sections.ShellExitMsg{Err: errors.New("pods/exec is forbidden")}, "pods/exec is forbidden"},
		{"no shell", sections.ShellExitMsg{NoShell: true}, "no shell in this container"},
		{"clean", sections.ShellExitMsg{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(&k8s.Manager{}, &config.AppConfig{})
			t.Cleanup(m.ctx.Cancel)
			t.Cleanup(m.shell.Close)
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m.shell.Reset("demo", "default", "pod", "")
			m.state = stateShellView
			m.Update(tc.msg)
			if tc.want == "" {
				require.Equal(t, stateMain, m.state)
				return
			}
			require.Equal(t, stateShellView, m.state)
			require.Contains(t, ansi.Strip(m.View().Content), tc.want)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			require.Equal(t, stateMain, m.state)
		})
	}
}
