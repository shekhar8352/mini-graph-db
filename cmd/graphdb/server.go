package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/shekhar8352/mini-graph-db/internal/config"
	"github.com/shekhar8352/mini-graph-db/internal/logging"
	"github.com/shekhar8352/mini-graph-db/internal/server"
	"github.com/shekhar8352/mini-graph-db/internal/version"
)

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Serve databases over gRPC",
		RunE:  runServer,
	}
	addServerFlags(cmd)
	return cmd
}

func addServerFlags(cmd *cobra.Command) {
	cmd.Flags().String("config", "", "TOML config file (default graphdb.toml when that file exists)")
	cmd.Flags().String("bind", "", "gRPC listen address")
	cmd.Flags().Bool("insecure-plaintext", false, "allow a non-loopback listener without TLS")
	cmd.Flags().String("pid-file", "", "pid file path (default <data-dir>/graphdb.pid)")
	cmd.Flags().String("tls-cert", "", "TLS certificate PEM")
	cmd.Flags().String("tls-key", "", "TLS private key PEM")
	cmd.Flags().String("tls-client-ca", "", "client CA PEM; when set, clients must present a certificate")
}

func runServer(cmd *cobra.Command, _ []string) error {
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
	if _, err := logging.Setup(logging.Config{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
		Out:    cmd.ErrOrStderr(),
	}); err != nil {
		return err
	}
	srv, err := server.Listen(cfg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "listening %s\n", srv.Addr()); err != nil {
		_ = srv.Shutdown(context.Background())
		return err
	}
	slog.Info("graphdb server", "version", version.Version, "config", used)
	for _, line := range cfg.RedactedLines() {
		slog.Info("effective config", "setting", line)
	}
	if !cfg.Storage.Fsync {
		slog.Warn("WAL fsync is disabled; acknowledged commits can be lost on crash")
	}
	if cfg.Server.InsecurePlaintext {
		slog.Warn("insecure plaintext is enabled")
	}
	slog.Warn("authentication is not enabled")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()
	select {
	case err := <-errc:
		shut, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shut)
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shut, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shut)
	}
}

func overlayFlags(cmd *cobra.Command) (config.Flags, error) {
	var f config.Flags
	var err error
	if cmd.Flags().Changed("bind") {
		f.BindSet = true
		f.Bind, err = cmd.Flags().GetString("bind")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed(flagDataDir) {
		f.DataDirSet = true
		f.DataDir, err = cmd.Flags().GetString(flagDataDir)
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed("insecure-plaintext") {
		f.InsecureSet = true
		f.InsecurePlaintext, err = cmd.Flags().GetBool("insecure-plaintext")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed("pid-file") {
		f.PIDFileSet = true
		f.PIDFile, err = cmd.Flags().GetString("pid-file")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed("tls-cert") {
		f.TLSCertSet = true
		f.TLSCert, err = cmd.Flags().GetString("tls-cert")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed("tls-key") {
		f.TLSKeySet = true
		f.TLSKey, err = cmd.Flags().GetString("tls-key")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed("tls-client-ca") {
		f.TLSClientCASet = true
		f.TLSClientCA, err = cmd.Flags().GetString("tls-client-ca")
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed(flagLogLevel) {
		f.LogLevelSet = true
		f.LogLevel, err = cmd.Flags().GetString(flagLogLevel)
		if err != nil {
			return f, err
		}
	}
	if cmd.Flags().Changed(flagLogFormat) {
		f.LogFormatSet = true
		f.LogFormat, err = cmd.Flags().GetString(flagLogFormat)
		if err != nil {
			return f, err
		}
	}
	return f, nil
}
