// Package server serves the graph database over gRPC.
package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/config"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Server is a gRPC endpoint in front of a Host.
type Server struct {
	host   *Host
	cfg    config.Config
	gs     *grpc.Server
	lis    net.Listener
	health *health.Server
	pid    string
}

// Listen opens databases, writes the pid file, and binds the address.
// Serve accepts connections. Shutdown stops them.
func Listen(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	host, err := OpenHost(cfg)
	if err != nil {
		return nil, err
	}
	pid := cfg.PIDPath()
	if err := acquirePID(pid); err != nil {
		_ = host.Close()
		return nil, err
	}
	lis, err := net.Listen("tcp", cfg.Server.Bind)
	if err != nil {
		_ = releasePID(pid)
		_ = host.Close()
		return nil, gerr.Wrap(gerr.Unavailable, "listen", err)
	}
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(cfg.Limits.MaxMessageBytes),
		grpc.MaxSendMsgSize(cfg.Limits.MaxMessageBytes),
		grpc.StatsHandler(host),
		grpc.ChainUnaryInterceptor(host.unary),
		grpc.ChainStreamInterceptor(host.stream),
	}
	if cfg.Server.TLS.Cert != "" {
		creds, err := serverCreds(cfg)
		if err != nil {
			_ = lis.Close()
			_ = releasePID(pid)
			_ = host.Close()
			return nil, err
		}
		opts = append(opts, grpc.Creds(creds))
	}
	gs := grpc.NewServer(opts...)
	graphdbv1.RegisterQueryServiceServer(gs, &queryServer{host: host})
	graphdbv1.RegisterTransactionServiceServer(gs, &txnServer{host: host})
	graphdbv1.RegisterAdminServiceServer(gs, &adminServer{host: host})
	hs := health.NewServer()
	healthpb.RegisterHealthServer(gs, hs)
	serving := healthpb.HealthCheckResponse_SERVING
	hs.SetServingStatus("", serving)
	for _, name := range []string{
		"graphdb.v1.QueryService",
		"graphdb.v1.TransactionService",
		"graphdb.v1.AdminService",
	} {
		hs.SetServingStatus(name, serving)
	}
	reflection.Register(gs)
	return &Server{host: host, cfg: cfg, gs: gs, lis: lis, health: hs, pid: pid}, nil
}

// Addr is the bound address, which may differ from the configured port when it is 0.
func (s *Server) Addr() string {
	if s == nil || s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}

// Serve accepts connections until Shutdown or Stop.
func (s *Server) Serve() error {
	err := s.gs.Serve(s.lis)
	if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting, drains until ctx ends, checkpoints, and closes databases.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.health != nil {
		s.health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}
	done := make(chan struct{})
	go func() {
		s.gs.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.gs.Stop()
		<-done
	}
	err := s.host.Close()
	if perr := releasePID(s.pid); perr != nil && err == nil {
		err = perr
	}
	return err
}

func serverCreds(cfg config.Config) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Server.TLS.Cert, cfg.Server.TLS.Key)
	if err != nil {
		return nil, gerr.Wrap(gerr.InvalidArgument, "load tls certificate", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if cfg.Server.TLS.ClientCA != "" {
		pem, err := os.ReadFile(cfg.Server.TLS.ClientCA)
		if err != nil {
			return nil, gerr.Wrap(gerr.InvalidArgument, "read client ca", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, gerr.New(gerr.InvalidArgument, "client ca has no certificates")
		}
		tlsCfg.ClientCAs = pool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return credentials.NewTLS(tlsCfg), nil
}
