package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// startPTY runs cmd as the session leader of a new pseudo-terminal of the
// given size. The returned file is the controller side; reads deliver the
// program's output and writes are its keyboard input.
func startPTY(cmd *exec.Cmd, cols, rows int) (*os.File, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	fd := int(ptmx.Fd())
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("unlockpt: %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("ptsname: %w", err)
	}
	if err = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: dim(cols), Row: dim(rows)}); err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("winsize: %w", err)
	}
	tty, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = ptmx.Close()
		return nil, err
	}
	defer func() { _ = tty.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err = cmd.Start(); err != nil {
		_ = ptmx.Close()
		return nil, err
	}
	return ptmx, nil
}

// dim clamps a terminal dimension to what the kernel's winsize holds.
func dim(n int) uint16 { return uint16(min(max(n, 1), 0xffff)) } // #nosec G115 -- Clamped to the uint16 range first.
