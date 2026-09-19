// Command graphdb is the mini-graph-db CLI (shell and version in Phase 0).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/logging"
	"github.com/shekhar8352/mini-graph-db/internal/version"
)

const (
	defaultDataDir  = "data"
	defaultSnapshot = "graph.db"
	defaultWAL      = "graph.wal"
	defaultHistory  = "graph.history"
	flagDataDir     = "data-dir"
	flagDB          = "db"
	flagWAL         = "wal"
	flagHistory     = "history"
	flagLogLevel    = "log-level"
	flagLogFormat   = "log-format"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "graphdb",
		Short:         "Embedded property-graph database",
		Long:          "graphdb is an embedded property-graph database with an interactive shell.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// `graphdb` with no subcommand starts the shell (Phase 0 default).
		RunE: runShell,
	}
	cmd.PersistentFlags().String(flagDataDir, defaultDataDir, "directory for snapshot, WAL, and history files")
	cmd.PersistentFlags().String(flagDB, "", "snapshot file (default <data-dir>/graph.db)")
	cmd.PersistentFlags().String(flagWAL, "", "append-only write-ahead log (default <data-dir>/graph.wal)")
	cmd.PersistentFlags().String(flagHistory, "", "REPL command history (default <data-dir>/graph.history)")
	cmd.PersistentFlags().String(flagLogLevel, "info", "log level: debug, info, warn, error")
	cmd.PersistentFlags().String(flagLogFormat, "text", "log format: text or json")

	cmd.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		level, err := c.Flags().GetString(flagLogLevel)
		if err != nil {
			return err
		}
		format, err := c.Flags().GetString(flagLogFormat)
		if err != nil {
			return err
		}
		_, err = logging.Setup(logging.Config{
			Level:  level,
			Format: format,
			Out:    c.ErrOrStderr(),
		})
		return err
	}

	cmd.AddCommand(newShellCmd(), newVersionCmd())
	cmd.CompletionOptions.DisableDefaultCmd = true
	return cmd
}

func newShellCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Start the embedded interactive shell",
		RunE:  runShell,
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, git commit, and build date",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write([]byte(version.String() + "\n"))
			return err
		},
	}
}
