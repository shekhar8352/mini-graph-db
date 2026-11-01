package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

type ctxKey int

const (
	connKey ctxKey = iota
	requestKey
)

type connState struct {
	id       uint64
	rejected atomic.Bool
}

// Metrics counts RPCs for a later Prometheus exporter.
type Metrics struct {
	Requests atomic.Int64
	Errors   atomic.Int64
	Panics   atomic.Int64
	Nanos    atomic.Int64
}

// TagConn implements grpc/stats.Handler. It stamps the connection id onto ctx.
func (h *Host) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	st := &connState{id: h.nextConn.Add(1)}
	return context.WithValue(ctx, connKey, st)
}

// HandleConn implements grpc/stats.Handler. The connection past max_connections is rejected.
func (h *Host) HandleConn(ctx context.Context, s stats.ConnStats) {
	st, _ := ctx.Value(connKey).(*connState)
	switch s.(type) {
	case *stats.ConnBegin:
		n := h.conns.Add(1)
		if st != nil && int(n) > h.cfg.Server.MaxConnections {
			st.rejected.Store(true)
		}
	case *stats.ConnEnd:
		h.conns.Add(-1)
		if st != nil {
			h.reg.dropConn(st.id)
		}
	}
}

// TagRPC implements grpc/stats.Handler. Deadlines are applied by the interceptors.
func (h *Host) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }

// HandleRPC implements grpc/stats.Handler. Per-call counters live in the interceptors.
func (h *Host) HandleRPC(context.Context, stats.RPCStats) {}

func (h *Host) sessionFrom(ctx context.Context) (*Session, error) {
	st, _ := ctx.Value(connKey).(*connState)
	if st == nil {
		return nil, gerr.New(gerr.Internal, "no connection")
	}
	if st.rejected.Load() {
		return nil, gerr.Newf(gerr.ResourceExhausted, "too many connections (max %d)", h.cfg.Server.MaxConnections)
	}
	return h.reg.session(st.id, h), nil
}

func (h *Host) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	ctx, id := h.decorate(ctx)
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			h.metrics.Panics.Add(1)
			err = gerr.Newf(gerr.Internal, "panic: %v", rec)
			resp = nil
		}
		err = h.finish(id, info.FullMethod, start, err)
	}()
	if err = h.reject(ctx); err != nil {
		return nil, err
	}
	resp, err = handler(ctx, req)
	return resp, err
}

func (h *Host) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	ctx, id := h.decorate(ss.Context())
	start := time.Now()
	defer func() {
		if rec := recover(); rec != nil {
			h.metrics.Panics.Add(1)
			err = gerr.Newf(gerr.Internal, "panic: %v", rec)
		}
		err = h.finish(id, info.FullMethod, start, err)
	}()
	if err = h.reject(ctx); err != nil {
		return err
	}
	err = handler(srv, &ctxStream{ServerStream: ss, ctx: ctx})
	return err
}

func (h *Host) decorate(ctx context.Context) (context.Context, string) {
	id := requestID(ctx)
	ctx = context.WithValue(ctx, requestKey, id)
	_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", id))
	return ctx, id
}

func (h *Host) reject(ctx context.Context) error {
	st, _ := ctx.Value(connKey).(*connState)
	if st != nil && st.rejected.Load() {
		return gerr.Newf(gerr.ResourceExhausted, "too many connections (max %d)", h.cfg.Server.MaxConnections)
	}
	return nil
}

func (h *Host) finish(id, method string, start time.Time, err error) error {
	err = statusFrom(err)
	elapsed := time.Since(start)
	h.metrics.Requests.Add(1)
	h.metrics.Nanos.Add(elapsed.Nanoseconds())
	code := status.Code(err)
	if code != 0 {
		h.metrics.Errors.Add(1)
	}
	slog.Info("rpc",
		"request_id", id,
		"method", method,
		"code", code.String(),
		"duration", elapsed,
	)
	return err
}

func requestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("x-request-id"); len(v) > 0 && v[0] != "" && len(v[0]) <= 128 {
			return v[0]
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(b[:])
}

type ctxStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *ctxStream) Context() context.Context { return s.ctx }
