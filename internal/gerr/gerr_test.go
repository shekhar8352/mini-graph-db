package gerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestRetryableCodes(t *testing.T) {
	retryable := map[Code]bool{
		Conflict:          true,
		Unavailable:       true,
		ResourceExhausted: true,
	}
	all := []Code{
		NotFound, AlreadyExists, InvalidArgument, Syntax, Semantic,
		ConstraintViolation, Conflict, Unauthenticated, PermissionDenied,
		ResourceExhausted, Corruption, Unavailable, Internal,
	}
	for _, c := range all {
		if got := c.Retryable(); got != retryable[c] {
			t.Errorf("%s Retryable()=%v want %v", c, got, retryable[c])
		}
	}
}

func TestNewErrorMessageAndCode(t *testing.T) {
	err := Newf(NotFound, "node %d not found", 9)
	if err.Code() != NotFound {
		t.Fatalf("code %s", err.Code())
	}
	if err.Retryable() {
		t.Fatal("NotFound must not be retryable")
	}
	if got := err.Error(); got != "NotFound: node 9 not found" {
		t.Fatalf("Error()=%q", got)
	}
	if !IsCode(err, NotFound) {
		t.Fatal("IsCode")
	}
	if Retryable(err) {
		t.Fatal("Retryable(err)")
	}
}

func TestWrapUnwrap(t *testing.T) {
	inner := errors.New("disk full")
	err := Wrap(ResourceExhausted, "write wal", inner)
	if !errors.Is(err, inner) {
		t.Fatal("unwrap")
	}
	if !err.Retryable() || !Retryable(err) {
		t.Fatal("ResourceExhausted should be retryable")
	}
	if CodeOf(err) != ResourceExhausted {
		t.Fatalf("CodeOf=%s", CodeOf(err))
	}
	if got := err.Error(); got != "ResourceExhausted: write wal: disk full" {
		t.Fatalf("Error()=%q", got)
	}
}

func TestErrorsIsByCode(t *testing.T) {
	err := New(Conflict, "write-write")
	if !errors.Is(err, New(Conflict, "other message")) {
		t.Fatal("same-code errors should match via errors.Is")
	}
	if errors.Is(err, New(NotFound, "write-write")) {
		t.Fatal("different codes must not match")
	}
}

func TestCodeOfUnclassified(t *testing.T) {
	if CodeOf(nil) != "" {
		t.Fatal("nil")
	}
	if CodeOf(fmt.Errorf("plain")) != Internal {
		t.Fatal("plain errors are Internal")
	}
}

func TestConflictRetryableThroughWrap(t *testing.T) {
	err := fmt.Errorf("txn: %w", New(Conflict, "first committer wins"))
	if !Retryable(err) {
		t.Fatal("wrapped Conflict must stay retryable")
	}
	if !IsCode(err, Conflict) {
		t.Fatal("IsCode through wrap")
	}
}
