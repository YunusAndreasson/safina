// Package localstate provides owner-only files for local broker state.
package localstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Directory refuses symlinks and directories owned by another user. Parent
// directories are trusted operator-selected paths, not untrusted references.
func Directory(path string) error {
	var created []string
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		created = append(created, current)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	var stat unix.Stat_t
	if err = unix.Lstat(path, &stat); err != nil {
		return err
	}
	if !info.IsDir() || int64(stat.Uid) != int64(os.Getuid()) {
		return errors.New("unsafe local state directory")
	}
	if err = os.Chmod(path, 0o700); err != nil { // #nosec G302 -- Owner-only directory requires search permission.
		return err
	}
	for _, current := range created {
		if err = SyncDirectory(filepath.Dir(current)); err != nil {
			return err
		}
	}
	return nil
}

func Open(path string, flags int) (*os.File, error) {
	// A FIFO must not block open before Fstat can reject non-regular files.
	fd, err := unix.Open(path, (flags&^unix.O_TRUNC)|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err == nil && (int64(stat.Uid) != int64(os.Getuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1) {
		err = errors.New("unsafe local state file")
	}
	if err == nil && flags&unix.O_NONBLOCK == 0 {
		err = unix.SetNonblock(fd, false)
	}
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil && flags&unix.O_TRUNC != 0 {
		err = f.Truncate(0)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func Lock(path string) (*os.File, error) {
	f, err := Open(path, unix.O_CREAT|unix.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock: %w", err)
	}
	return f, nil
}

func SyncDirectory(path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
