package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

func TestSessionPreservesExitFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		noShell bool
	}{
		{"clean", nil, false},
		{"startup exit 127", utilexec.CodeExitError{Err: errors.New("command terminated with exit code 127"), Code: 127}, false},
		{"nonzero exit", utilexec.CodeExitError{Err: errors.New("command terminated with exit code 1"), Code: 1}, false},
		{"missing shell", errors.New("exec: sh: executable file not found in $PATH"), true},
		{"permission denied", errors.New("pods/exec is forbidden"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := startSession(t.Context(), inputExecutor{func(_ context.Context, opts remotecommand.StreamOptions) error {
				_, err := fmt.Fprint(opts.Stdout, "startup diagnostics\r\n")
				if err != nil {
					return err
				}
				return tc.err
			}})
			defer session.Close()
			var output strings.Builder
			var end ShellEvent
			for event := range session.Events {
				output.Write(event.Bytes)
				if event.Done {
					end = event
				}
			}
			require.Equal(t, "startup diagnostics\r\n", output.String())
			require.True(t, end.Done)
			require.Equal(t, tc.err, end.Err)
			require.Equal(t, tc.noShell, end.NoShell)
		})
	}
}
