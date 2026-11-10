package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// clockTicks is USER_HZ, which Linux fixes at 100 on every supported arch.
const clockTicks = 100

func killGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// ProcStat is the CPU time and memory of a process and all its descendants.
type ProcStat struct {
	CPU   time.Duration // user + system
	RSS   int64         // bytes, current
	HWM   int64         // bytes, sum of each process's peak resident set
	Procs int
}

func readStat(pid int) (ppid int, cpu time.Duration, ok bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, false
	}
	// The command name may contain spaces; fields resume after the last ')'.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 13 {
		return 0, 0, false
	}
	ppid, _ = strconv.Atoi(f[1])
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	st, _ := strconv.ParseInt(f[12], 10, 64)
	return ppid, time.Duration(ut+st) * time.Second / clockTicks, true
}

func readMem(pid int) (rss, hwm int64) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		var dst *int64
		switch {
		case strings.HasPrefix(line, "VmRSS:"):
			dst = &rss
		case strings.HasPrefix(line, "VmHWM:"):
			dst = &hwm
		default:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			*dst = kb << 10
		}
	}
	return rss, hwm
}

// owned reports whether a process's command line mentions marker. A tool's
// daemon (safina's broker) detaches from its tree but keeps paths under the
// tool's private home in its arguments.
func owned(pid int, marker string) bool {
	if marker == "" {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	return err == nil && bytes.Contains(b, []byte(marker))
}

// Owned lists the processes, other than root's tree, that belong to marker.
func Owned(marker string) []int {
	var pids []int
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		if pid, err := strconv.Atoi(filepath.Base(d)); err == nil && pid != os.Getpid() && owned(pid, marker) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// TreeStat sums the process tree rooted at pid plus every process whose
// command line mentions marker. Children and detached daemons count against
// the tool that started them.
func TreeStat(root int, marker string) ProcStat {
	parents := map[int]int{}
	cpus := map[int]time.Duration{}
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil {
			continue
		}
		if ppid, cpu, ok := readStat(pid); ok {
			parents[pid], cpus[pid] = ppid, cpu
		}
	}
	in := func(pid int) bool {
		for p := pid; p > 1; p = parents[p] {
			if p == root || owned(p, marker) {
				return true
			}
		}
		return false
	}
	var out ProcStat
	for pid := range cpus {
		if !in(pid) {
			continue
		}
		rss, hwm := readMem(pid)
		out.CPU += cpus[pid]
		out.RSS += rss
		out.HWM += hwm
		out.Procs++
	}
	return out
}
