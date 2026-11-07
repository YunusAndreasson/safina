package history

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// quotaLock coordinates independent Safina processes. A crashed writer releases
// both this lock and its segment/capture locks automatically.
func (s *Store) quotaLock() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(s.opts.Directory, "quota.lock"), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- Operator-configured history root; fixed basename.
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// capacity runs under the process mutex and quota lock. Open evidence files
// reserve their entire capture allowance, avoiding per-line directory scans.
func (s *Store) capacity(pool string, needed int64) error {
	limit := s.opts.ActionBytes
	if pool == "evidence" {
		limit = s.opts.EvidenceBytes
	}
	root, err := os.OpenRoot(s.opts.Directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	var total int64
	var removable []retainedFile
	err = filepath.WalkDir(filepath.Join(s.opts.Directory, pool), func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(s.opts.Directory, path)
		if e != nil {
			return e
		}
		f, e := root.OpenFile(relative, os.O_RDWR, 0)
		if e != nil {
			return e
		} // #nosec G304 -- Enumerated local history file; symlinks excluded above.
		locked := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
		_ = f.Close()
		size := info.Size()
		if !locked && pool == "evidence" {
			size = max(size, int64(CaptureBytes))
		}
		total += size
		if locked {
			removable = append(removable, retainedFile{path, size, info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(removable, func(i, j int) bool { return removable[i].at.Before(removable[j].at) })
	for _, f := range removable {
		if total+needed <= limit {
			break
		}
		if err = os.Remove(f.path); err != nil {
			return err
		}
		_ = os.Remove(f.path + ".index")
		total -= f.size
		marker := time.Now().UTC().Format(time.RFC3339Nano) + ": " + pool + " capacity pruning; older evidence/history unavailable\n"
		if err = os.WriteFile(filepath.Join(s.opts.Directory, pool+"-retention.txt"), []byte(marker), 0o600); err != nil {
			return err
		}
	}
	if total+needed > limit {
		return fmt.Errorf("%s capacity reached; recording incomplete", pool)
	}
	return nil
}
