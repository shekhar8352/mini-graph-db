package server

import (
	"context"
	"errors"
	"regexp"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	graphdbv1 "github.com/shekhar8352/mini-graph-db/api/gen/graphdb/v1"
	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

var posRe = regexp.MustCompile(`at (\d+):(\d+)`)

func statusFrom(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return withInfo(status.New(codes.DeadlineExceeded, err.Error()), gerr.ResourceExhausted, false, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	code := gerr.CodeOf(err)
	return withInfo(status.New(grpcCode(code), err.Error()), code, gerr.Retryable(err), err.Error())
}

func grpcCode(code gerr.Code) codes.Code {
	switch code {
	case gerr.NotFound:
		return codes.NotFound
	case gerr.AlreadyExists:
		return codes.AlreadyExists
	case gerr.InvalidArgument, gerr.Syntax, gerr.Semantic:
		return codes.InvalidArgument
	case gerr.ConstraintViolation:
		return codes.FailedPrecondition
	case gerr.Conflict:
		return codes.Aborted
	case gerr.Unauthenticated:
		return codes.Unauthenticated
	case gerr.PermissionDenied:
		return codes.PermissionDenied
	case gerr.ResourceExhausted:
		return codes.ResourceExhausted
	case gerr.Corruption:
		return codes.DataLoss
	case gerr.Unavailable:
		return codes.Unavailable
	default:
		return codes.Internal
	}
}

func withInfo(st *status.Status, code gerr.Code, retryable bool, msg string) error {
	info := &graphdbv1.ErrorInfo{Code: string(code), Retryable: retryable}
	if line, col, ok := positionOf(msg); ok {
		info.Position = &graphdbv1.Position{Line: line, Column: col}
	}
	out, err := st.WithDetails(info)
	if err != nil {
		return st.Err()
	}
	return out.Err()
}

func positionOf(msg string) (int32, int32, bool) {
	m := posRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, 0, false
	}
	line, err1 := strconv.Atoi(m[1])
	col, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int32(line), int32(col), true
}
