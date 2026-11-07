package localstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRejectSymlinksAndHardlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.WriteFile(target, []byte("preserve"), 0o600))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(target, link))
	_, err := Open(link, unix.O_RDWR|unix.O_TRUNC)
	require.Error(t, err)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Link(target, link))
	_, err = Open(link, unix.O_RDWR|unix.O_TRUNC)
	require.Error(t, err)
	dirLink := filepath.Join(root, "dir-link")
	require.NoError(t, os.Symlink(root, dirLink))
	require.Error(t, Directory(dirLink))
	b, err := os.ReadFile(target) // #nosec G304 -- Test-owned temporary file.
	require.NoError(t, err)
	require.Equal(t, "preserve", string(b))
}

func TestFIFORejectedWithoutWaitingForPeer(t *testing.T) {
	for _, flags := range []int{unix.O_RDONLY, unix.O_WRONLY} {
		path := filepath.Join(t.TempDir(), "broker.log")
		require.NoError(t, unix.Mkfifo(path, 0o600))
		done := make(chan error, 1)
		go func() {
			f, err := Open(path, flags)
			if f != nil {
				_ = f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			require.Error(t, err)
		case <-time.After(200 * time.Millisecond):
			// Release the original blocking open so the failed probe leaks no worker.
			peer, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
			require.NoError(t, err)
			require.Error(t, <-done)
			require.NoError(t, unix.Close(peer))
			t.Error("unsafe FIFO open waited for a peer before rejecting the file")
		}
	}
}
