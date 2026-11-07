package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/remotecommand"
)

func TestTerminalSizeDoesNotWrap(t *testing.T) {
	for _, tt := range []struct {
		cols, rows int
		want       remotecommand.TerminalSize
	}{
		{80, 24, remotecommand.TerminalSize{Width: 80, Height: 24}},
		{65536, 70000, remotecommand.TerminalSize{Width: 65535, Height: 65535}},
		{0, -1, remotecommand.TerminalSize{Width: 1, Height: 1}},
	} {
		assert.Equal(t, tt.want, terminalSize(tt.cols, tt.rows))
	}
}
