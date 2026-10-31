package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/server"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative utilities",
	}
	cmd.AddCommand(newGenCertCmd())
	return cmd
}

func newGenCertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen-cert",
		Short: "Write a self-signed TLS certificate for local development",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cert, err := cmd.Flags().GetString("cert")
			if err != nil {
				return err
			}
			key, err := cmd.Flags().GetString("key")
			if err != nil {
				return err
			}
			hosts, err := cmd.Flags().GetStringArray("host")
			if err != nil {
				return err
			}
			if err := server.WriteDevCert(cert, key, hosts); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s and %s\n", cert, key)
			return err
		},
	}
	cmd.Flags().String("cert", "graphdb.crt", "certificate PEM path")
	cmd.Flags().String("key", "graphdb.key", "private key PEM path")
	cmd.Flags().StringArray("host", []string{"localhost", "127.0.0.1", "::1"}, "DNS name or IP address (repeatable)")
	return cmd
}
