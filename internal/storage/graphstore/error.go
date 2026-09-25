package graphstore

import (
	"errors"
	"fmt"
)

// Error is a graphstore failure the graph facade maps onto gerr.
type Error struct {
	kind string
	msg  string
}

// Error returns the failure text.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.msg
}

// NotFound reports a missing node or edge.
func (e *Error) NotFound() bool { return e != nil && e.kind == "notfound" }

// Invalid reports a rejected argument, such as an unstorable property.
func (e *Error) Invalid() bool { return e != nil && e.kind == "invalid" }

// IsNotFound reports whether err is a missing node or edge.
func IsNotFound(err error) bool {
	var ge *Error
	return errors.As(err, &ge) && ge.NotFound()
}

func notFound(what string, id uint64) error {
	return &Error{kind: "notfound", msg: fmt.Sprintf("%s %d not found", what, id)}
}

func invalidf(format string, args ...any) error {
	return &Error{kind: "invalid", msg: fmt.Sprintf(format, args...)}
}

func corrupt(what string) error {
	return fmt.Errorf("graphstore: corrupt %s", what)
}
