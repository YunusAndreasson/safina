package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/YunusAndreasson/safina/internal/broker"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/debug"
	"github.com/YunusAndreasson/safina/internal/errlog"
	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui"
	"github.com/YunusAndreasson/safina/internal/version"
)

var (
	kubeconfig string
	context    string
	namespace  string
	debugFlag  bool
	logFile    string
	runtimeDir string
	stateDir   string
	historyDir string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "safina",
		Version: version.Version,
		Short:   "🚢 Safina – A lightweight Kubernetes TUI navigator",
		Long: `Safina is a terminal-based Kubernetes navigation tool that allows
operators to browse multiple clusters, list namespaces, view pods with
color-coded status, and perform actions like scaling, rollback, and log viewing.`,
		Run: func(cmd *cobra.Command, args []string) {
			// Flags are sugar over the existing env-var contract so a user can
			// flip on debug without remembering SAFINA_DEBUG. The env vars
			// stay authoritative if both are set — env wins, matching the
			// usual 12-factor expectation.
			if debugFlag && os.Getenv("SAFINA_DEBUG") == "" {
				_ = os.Setenv("SAFINA_DEBUG", "1")
			}
			if logFile != "" && os.Getenv("SAFINA_DEBUG_FILE") == "" {
				_ = os.Setenv("SAFINA_DEBUG_FILE", logFile)
			}
			// errlog comes up before anything that could trigger a klog
			// emission (manager init opens client-go transports). It's
			// always on — point is post-mortem evaluation, not a debug
			// opt-in — and it captures client-go's stderr noise plus
			// mirrored debug.Errf events.
			errPath := errlog.Init()
			defer errlog.Close()

			debugPath := debug.Init()
			defer debug.Close()
			debug.Log("safina.start",
				"kubeconfig_flag", kubeconfig,
				"context_flag", context,
				"namespace_flag", namespace)

			appCfg, _ := config.Load()
			if historyDir != "" {
				appCfg.History.Directory = historyDir
			}
			journal, historyErr := history.Open(appCfg.History)
			defer func() { _ = journal.Close() }()

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

			mgr.History = journal
			model := tui.NewModel(mgr, appCfg)
			model.SetBrokerOptions(broker.Options{Kubeconfig: kubeconfig, RuntimeDir: runtimeDir, StateDir: stateDir, History: appCfg.History})
			if historyErr != nil {
				model.SetStartupBanner(journal.Health())
			}
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
			if errPath != "" {
				fmt.Fprintf(os.Stderr, "safina error log: %s\n", errPath)
			}
		},
	}

	rootCmd.AddCommand(historyCommand())
	rootCmd.AddCommand(brokerCommands()...)
	rootCmd.AddCommand(mcpCommand())
	rootCmd.Flags().StringVar(&runtimeDir, "runtime-dir", "", "Owner-only broker runtime directory")
	rootCmd.Flags().StringVar(&stateDir, "state-dir", "", "Durable investigation directory")
	rootCmd.Flags().StringVar(&historyDir, "history-dir", "", "Logbook directory")
	rootCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	rootCmd.Flags().StringVar(&context, "context", "", "Kubernetes context to use")
	rootCmd.Flags().StringVar(&namespace, "namespace", "", "Kubernetes namespace to use")
	rootCmd.Flags().BoolVar(&debugFlag, "debug", false, "Write JSON debug log (sets SAFINA_DEBUG=1)")
	rootCmd.Flags().StringVar(&logFile, "log-file", "", "Debug log path (default ~/.safina/debug.log)")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func historyCommand() *cobra.Command {
	parent := &cobra.Command{Use: "history", Short: "Inspect saved Safina activity without cluster access"}
	for _, verb := range []string{"list", "show", "export"} {
		var f history.Filter
		var since, until, format, out string
		command := &cobra.Command{Use: verb, Short: verb + " saved history", RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			opts := cfg.History.Defaults()
			var err error
			if f.Limit < 1 || f.Limit > 1000000 {
				return errors.New("limit must be between 1 and 1000000")
			}
			if since != "" {
				f.Since, err = time.Parse(time.RFC3339, since)
				if err != nil {
					return err
				}
			}
			if until != "" {
				f.Until, err = time.Parse(time.RFC3339, until)
				if err != nil {
					return err
				}
			}
			if !f.Since.IsZero() && !f.Until.IsZero() && f.Until.Before(f.Since) {
				return errors.New("until must be after since")
			}
			if verb == "show" {
				if len(args) != 1 {
					return errors.New("show requires an event or operation ID")
				}
				f.ID = args[0]
			} else if len(args) != 0 {
				return errors.New("unexpected arguments")
			}
			if verb == "export" {
				if out == "" {
					return errors.New("export requires --output directory")
				}
				return history.Export(opts.Directory, out, f)
			}
			result, err := history.Query(opts.Directory, f)
			if err != nil {
				return err
			}
			switch format {
			case "jsonl":
				enc := json.NewEncoder(cmd.OutOrStdout())
				for _, r := range result.Records {
					if err = enc.Encode(r); err != nil {
						return err
					}
				}
			case "text":
				_, err = fmt.Fprint(cmd.OutOrStdout(), history.Summary(result, 16<<20))
			default:
				return errors.New("format must be text or jsonl")
			}
			for _, w := range result.Warnings {
				cmd.PrintErrln(w)
			}
			if result.Omitted > 0 {
				cmd.PrintErrf("%d older records omitted; narrow time filters or increase --limit\n", result.Omitted)
			}
			return err
		}}
		flags := command.Flags()
		flags.StringVar(&f.Cluster, "cluster", "", "Cluster ID or context alias")
		flags.StringVar(&f.Namespace, "namespace", "", "Namespace")
		flags.StringVar(&f.Resource, "resource", "", "Resource name or Kind/name")
		flags.StringVar(&f.Session, "session", "", "Safina session ID")
		flags.StringVar(&since, "since", "", "Inclusive UTC/RFC3339 start")
		flags.StringVar(&until, "until", "", "Inclusive UTC/RFC3339 end")
		flags.IntVar(&f.Limit, "limit", 10000, "Maximum newest records")
		flags.StringVar(&format, "format", "text", "text or jsonl")
		flags.StringVar(&out, "output", "", "New export directory")
		parent.AddCommand(command)
	}
	return parent
}
