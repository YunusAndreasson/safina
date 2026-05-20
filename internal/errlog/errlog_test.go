package errlog

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

// TestInit_CapturesKlogAndStderrSilent verifies the two-step muzzle:
// after Init, klog writes land in the file (not stderr). The regression
// this prevents is client-go errors painting over the TUI's alt-screen.
func TestInit_CapturesKlogAndStderrSilent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "errors.log")
	t.Setenv("SAFINA_ERROR_FILE", path)

	got := Init()
	if got != path {
		t.Fatalf("Init returned %q, want %q", got, path)
	}
	t.Cleanup(Close)

	// Swap os.Stderr to a pipe so we can prove klog isn't leaking onto
	// fd 2 after Init. Bubble Tea's alt-screen can't hide stderr, so the
	// muzzle is load-bearing — a regression here is exactly the bug that
	// painted "request.go:1196 context canceled" all over the TUI frame.
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	klog.Errorf("synthetic error: %s", "context canceled")
	klog.Flush()

	_ = w.Close()
	stderrBytes, _ := io.ReadAll(r)
	if len(stderrBytes) != 0 {
		t.Errorf("klog leaked to stderr: %q", stderrBytes)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read errlog: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "synthetic error: context canceled") {
		t.Errorf("klog error not captured in errlog. Got:\n%s", out)
	}
	if !strings.Contains(out, "safina session start") {
		t.Errorf("session marker missing. Got:\n%s", out)
	}
}

func TestWritef_MirrorsSafinaErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAFINA_ERROR_FILE", filepath.Join(dir, "errors.log"))

	Init()
	t.Cleanup(Close)

	Writef("k8s.list_partial", "kind=%s err=%s", "ConfigMap", "boom")

	b, _ := os.ReadFile(Path())
	out := string(b)
	if !strings.Contains(out, `msg=k8s.list_partial`) ||
		!strings.Contains(out, `kind=ConfigMap err=boom`) ||
		!strings.Contains(out, `origin=safina`) {
		t.Errorf("safina error not in expected format. Got:\n%s", out)
	}
}
