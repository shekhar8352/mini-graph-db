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
	for _, p := range []string{paths.db, paths.wal, paths.history} {
		if err := ensureParent(p); err != nil {
			return err
		}
	}
	slog.Info("starting embedded shell",
		"version", version.Version,
		"db", paths.db,
		"wal", paths.wal,
		"history", paths.history,
	)
	return repl.Run(repl.Config{
		DBPath:      paths.db,
		WALPath:     paths.wal,
		HistoryPath: paths.history,
		In:          cmd.InOrStdin(),
		Out:         cmd.OutOrStdout(),
	})
}

type dataPaths struct {
	db, wal, history string
}

func resolvePaths(cmd *cobra.Command) (dataPaths, error) {
	dataDir, err := cmd.Flags().GetString(flagDataDir)
	if err != nil {
		return dataPaths{}, err
	}
	db, err := cmd.Flags().GetString(flagDB)
	if err != nil {
		return dataPaths{}, err
	}
	wal, err := cmd.Flags().GetString(flagWAL)
	if err != nil {
		return dataPaths{}, err
	}
	hist, err := cmd.Flags().GetString(flagHistory)
	if err != nil {
		return dataPaths{}, err
	}
	if !cmd.Flags().Changed(flagDB) {
		db = filepath.Join(dataDir, defaultSnapshot)
	}
	if !cmd.Flags().Changed(flagWAL) {
		wal = filepath.Join(dataDir, defaultWAL)
	}
	if !cmd.Flags().Changed(flagHistory) {
		hist = filepath.Join(dataDir, defaultHistory)
	}
	return dataPaths{db: db, wal: wal, history: hist}, nil
}

func ensureParent(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
