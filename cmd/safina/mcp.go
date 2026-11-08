package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/YunusAndreasson/safina/internal/broker"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func mcpCommand() *cobra.Command {
	var o broker.Options
	var historyDir string
	cmd := &cobra.Command{Use: "mcp", Short: "Serve investigation-only MCP tools over stdio", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true}
	cmd.Flags().StringVar(&historyDir, "history-dir", "", "Logbook directory")
	cmd.Flags().StringVar(&o.Kubeconfig, "kubeconfig", "", "Path to kubeconfig")
	cmd.Flags().StringVar(&o.RuntimeDir, "runtime-dir", "", "Broker runtime directory")
	cmd.Flags().StringVar(&o.StateDir, "state-dir", "", "Investigation state directory")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		o.History = cfg.History
		if historyDir != "" {
			o.History.Directory = historyDir
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return mcpserver.New(broker.NewClient(o)).Run(ctx, &mcp.StdioTransport{MaxLineLength: 256 << 10})
	}
	return cmd
}
