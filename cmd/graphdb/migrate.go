package main

import (
	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/compat/gobimport"
)

func newMigrateCmd() *cobra.Command {
	var snapshot, walPath, dest string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Import a legacy gob snapshot into a disk database",
		Long: `Reads a legacy gob snapshot and an optional text WAL of query lines,
replays that log, and writes a new disk database directory.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return gobimport.Import(snapshot, walPath, dest)
		},
	}
	cmd.Flags().StringVar(&snapshot, "from-legacy", "", "legacy gob snapshot")
	cmd.Flags().StringVar(&walPath, "wal", "", "legacy text WAL of query lines")
	cmd.Flags().StringVar(&dest, "to", "", "new database directory")
	_ = cmd.MarkFlagRequired("from-legacy")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}
