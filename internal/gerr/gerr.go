// Package gerr defines the typed error taxonomy used across graphdb layers.
// Codes are stable strings and will map to gRPC status codes in Phase 6.
package gerr

import (
	"errors"
	"fmt"
)

// Code classifies an error for callers, logs, and (later) the wire protocol.
type Code string

// Classified error codes used across layers. Names are stable.
const (
	NotFound            Code = "NotFound"
	AlreadyExists       Code = "AlreadyExists"
	InvalidArgument     Code = "InvalidArgument"
	Syntax              Code = "Syntax"
	Semantic            Code = "Semantic"
	ConstraintViolation Code = "ConstraintViolation"
	Conflict            Code = "Conflict"
	Unauthenticated     Code = "Unauthenticated"
	PermissionDenied    Code = "PermissionDenied"
	ResourceExhausted   Code = "ResourceExhausted"
	Corruption          Code = "Corruption"
	Unavailable         Code = "Unavailable"
	Internal            Code = "Internal"
)

// Retryable reports whether an operation that failed with this code is
// safe to retry (possibly after backoff). Conflict is first-committer-wins
// and should be retried by the client; Unavailable and ResourceExhausted
// are typically transient.
func (c Code) Retryable() bool {
	switch c {
	case Conflict, Unavailable, ResourceExhausted:
		return true
	default:
		return false
	}
}

// Error is a classified graphdb error.
type Error struct {
	code    Code
	message string
	err     error
}

// New returns a new classified error with a message.
func New(code Code, message string) *Error {
	return &Error{code: code, message: message}
}

// Newf returns a new classified error with a formatted message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{code: code, message: fmt.Sprintf(format, args...)}
}

// Wrap classifies err. If message is empty, err's text is used.
func Wrap(code Code, message string, err error) *Error {
	return &Error{code: code, message: message, err: err}
}

// Code returns the classification.
func (e *Error) Code() Code {
	if e == nil {
		return Internal
	}
	return e.code
}

// Retryable reports whether this error is retryable.
func (e *Error) Retryable() bool {
	if e == nil {
		return false
	}
	return e.code.Retryable()
}

// Error implements the error interface. The code is included so operators
// and tests can see the taxonomy without unwrapping.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.err != nil && e.message != "":
		return fmt.Sprintf("%s: %s: %v", e.code, e.message, e.err)
	case e.err != nil:
		return fmt.Sprintf("%s: %v", e.code, e.err)
	case e.message != "":
		return fmt.Sprintf("%s: %s", e.code, e.message)
	default:
		return string(e.code)
	}
}

// Unwrap returns the wrapped cause, if any.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Is reports whether target is an *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || e == nil || t == nil {
		return false
	}
	return e.code == t.code
}

// CodeOf returns the classification of err, or Internal if err is not a
// classified graphdb error. A nil error yields a zero Code.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Code()
	}
	return Internal
}

// IsCode reports whether err is classified as code.
func IsCode(err error, code Code) bool {
	return CodeOf(err) == code && err != nil
}

// Retryable reports whether err is a retryable classified error.
func Retryable(err error) bool {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Retryable()
	}
	return false
}
