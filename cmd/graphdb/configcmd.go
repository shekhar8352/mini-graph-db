package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate or print configuration",
	}
	cmd.AddCommand(newConfigValidateCmd(), newConfigDefaultsCmd())
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Load configuration and report errors",
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags, err := overlayFlags(cmd)
			if err != nil {
				return err
			}
			path, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			cfg, used, err := config.LoadOptional(path, cmd.Flags().Changed("config"), flags)
			if err != nil {
				return err
			}
			if used == "" {
				used = "(defaults)"
			}
			for _, line := range cfg.RedactedLines() {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", line); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "config ok (%s)\n", used)
			return err
		},
	}
	addServerFlags(cmd)
	return cmd
}

func newConfigDefaultsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print-defaults",
		Short: "Print the default graphdb.toml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), config.DefaultTOML())
			return err
		},
	}
}
