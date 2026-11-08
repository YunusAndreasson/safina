package main

import (
	"encoding/json"
	"os"
	"os/signal"
	"syscall"

	"github.com/YunusAndreasson/safina/internal/broker"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/spf13/cobra"
)

func brokerCommands() []*cobra.Command {
	serve := &cobra.Command{Use: "serve", Short: "Run the local investigation broker", Args: cobra.NoArgs}
	control := &cobra.Command{Use: "broker", Short: "Inspect or stop the local investigation broker"}
	for _, verb := range []string{"status", "stop"} {
		command := &cobra.Command{Use: verb, Args: cobra.NoArgs}
		var o broker.Options
		command.Flags().StringVar(&o.RuntimeDir, "runtime-dir", "", "Owner-only broker runtime directory")
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			if verb == "stop" {
				return broker.Stop(cmd.Context(), o)
			}
			status, err := broker.BrokerStatus(cmd.Context(), o)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		}
		control.AddCommand(command)
	}
	var o broker.Options
	var historyDir string
	serve.Flags().StringVar(&o.RuntimeDir, "runtime-dir", "", "Owner-only broker runtime directory")
	serve.Flags().StringVar(&o.StateDir, "state-dir", "", "Durable investigation directory (independent of history retention)")
	serve.Flags().StringVar(&o.Kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	serve.Flags().StringVar(&historyDir, "history-dir", "", "Logbook delivery directory")
	serve.RunE = func(cmd *cobra.Command, _ []string) error {
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
		return broker.Serve(ctx, o)
	}
	return []*cobra.Command{serve, control}
}
