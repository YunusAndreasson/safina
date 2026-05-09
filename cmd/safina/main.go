package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui"
)

var (
	kubeconfig string
	context    string
	namespace  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "safina",
		Short: "🚢 Safina – A lightweight Kubernetes TUI navigator",
		Long: `Safina is a terminal-based Kubernetes navigation tool that allows
operators to browse multiple clusters, list namespaces, view pods with
color-coded status, and perform actions like scaling, rollback, and log viewing.`,
		Run: func(cmd *cobra.Command, args []string) {
			debugPath := debug.Init()
			defer debug.Close()
			debug.Log("safina.start",
				"kubeconfig_flag", kubeconfig,
				"context_flag", context,
				"namespace_flag", namespace)

			appCfg, _ := config.Load()

			rawCfg, err := config.LoadKubeConfig(kubeconfig)
			if err != nil {
				debug.Errf("kubeconfig.load_failed", "err", err.Error())
				fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
				os.Exit(1)
			}

			contexts := config.ListContexts(rawCfg)
			debug.Log("kubeconfig.loaded", "contexts", len(contexts))

			mgr, err := k8s.NewManager(rawCfg)
			if err != nil {
				debug.Errf("manager.init_failed", "err", err.Error())
				fmt.Fprintf(os.Stderr, "Error creating Kubernetes client: %v\n", err)
				os.Exit(1)
			}
			skipNames := make([]string, len(mgr.Skipped))
			for i, s := range mgr.Skipped {
				skipNames[i] = s.Name
			}
			debug.Log("manager.ready",
				"clients", len(mgr.Clients),
				"skipped", len(mgr.Skipped),
				"skipped_names", skipNames)

			model := tui.NewModel(mgr, appCfg)
			model.SetContexts(contexts)

			if context != "" {
				matched := false
				for _, c := range contexts {
					if c.Name == context {
						model.SetActiveContext(c.Name)
						matched = true
						break
					}
				}
				if !matched {
					// Surface the typo at first paint instead of silently
					// dropping the user at the picker wondering why their
					// flag was ignored.
					model.SetStartupBanner(fmt.Sprintf("--context=%q not found in kubeconfig", context))
					debug.Errf("flag.context_not_found", "requested", context)
				}
			}

			if namespace != "" {
				model.SetActiveNamespace(namespace)
			}

			// Bubble Tea v2 moves AltScreen + MouseMode into declarative
			// tea.View fields (set in Model.View). NewProgram only needs the
			// model itself; the runtime applies whatever the View frame says.
			p := tea.NewProgram(&model)
			if _, err := p.Run(); err != nil {
				debug.Errf("tui.run_failed", "err", err.Error())
				fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
				os.Exit(1)
			}
			debug.Log("safina.exit")
			// Surface the log path on graceful exit so the user knows where to
			// look. Bubble Tea has restored the original screen by now, so
			// stderr writes are visible above the shell prompt instead of
			// fighting the alt-screen frame.
			if debugPath != "" {
				fmt.Fprintf(os.Stderr, "safina debug log: %s\n", debugPath)
			}
		},
	}

	rootCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	rootCmd.Flags().StringVar(&context, "context", "", "Kubernetes context to use")
	rootCmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace to use")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
