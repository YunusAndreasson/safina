package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// Env is what every launch of a tool shares within one run: the proxied
// kubeconfig and a private home, so neither tool reads the user's config,
// skins, caches or history.
type Env struct {
	Kubeconfig string
	Context    string
	Home       string
	Namespace  string
}

type Tool struct {
	Name    string
	Bin     string
	Version string
	command func(t *Tool, e Env) *exec.Cmd
	script  Script
}

func (t *Tool) Command(e Env) *exec.Cmd { return t.command(t, e) }

func baseEnv(home string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=C.UTF-8",
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local/state"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local/share"),
	}
}

func safinaCommand(t *Tool, e Env) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), t.Bin, "--kubeconfig", e.Kubeconfig, "--context", e.Context) // #nosec G204 -- The operator-chosen binary under test.
	cmd.Env = baseEnv(e.Home)
	return cmd
}

// k9s runs read-only with its default refresh rate (2s) and skin. Logo and
// splash are off: the splash would only add a fixed delay to its launch.
func k9sCommand(t *Tool, e Env) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), t.Bin, "--kubeconfig", e.Kubeconfig, "--context", e.Context, // #nosec G204 -- The operator-chosen binary under test.
		"--readonly", "--logoless", "--splashless", "--command", "pods", "--all-namespaces")
	cmd.Env = append(baseEnv(e.Home), "K9S_CONFIG_DIR="+filepath.Join(e.Home, ".config/k9s"))
	return cmd
}
