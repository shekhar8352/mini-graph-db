package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/repl"
	"github.com/shekhar8352/mini-graph-db/internal/version"
)

func runShell(cmd *cobra.Command, _ []string) error {
	paths, err := resolvePaths(cmd)
	if err != nil {
		return err
	}
	if err := ensureParent(paths.history); err != nil {
		return err
	}
	slog.Info("starting embedded shell",
		"version", version.Version,
		"history", paths.history,
	)
	return repl.Run(repl.Config{
		HistoryPath: paths.history,
		In:          cmd.InOrStdin(),
		Out:         cmd.OutOrStdout(),
	})
}

type dataPaths struct {
	history string
}

func resolvePaths(cmd *cobra.Command) (dataPaths, error) {
	dataDir, err := cmd.Flags().GetString(flagDataDir)
	if err != nil {
		return dataPaths{}, err
	}
	hist, err := cmd.Flags().GetString(flagHistory)
	if err != nil {
		return dataPaths{}, err
	}
	if !cmd.Flags().Changed(flagHistory) {
		hist = filepath.Join(dataDir, defaultHistory)
	}
	return dataPaths{history: hist}, nil
}

func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
