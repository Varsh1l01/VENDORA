// Package apperr defines the application error model shared by all services.
//
// Domain code returns *Error values with a Kind. Transport layers translate
// Kinds to gRPC codes (ToStatus) and gRPC codes to HTTP statuses (in the
// gateway). Only the public Message is ever sent to clients; the wrapped
// cause is for logs.
package apperr

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Kind classifies an error independent of transport.
type Kind int

const (
	KindInternal Kind = iota
	KindInvalid
	KindNotFound
	KindConflict
	KindUnauthenticated
	KindForbidden
	KindFailedPrecondition
	KindUnavailable
	KindRateLimited
)

// Error is a classified application error.
type Error struct {
	Kind    Kind
	Message string // safe to show to clients
	Err     error  // underlying cause, never shown to clients
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// Is makes errors.Is(err, &Error{Kind: K}) match on Kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Message == "" && t.Err == nil && t.Kind == e.Kind
}

func newf(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Message: fmt.Sprintf(format, args...)}
}

func Invalid(format string, args ...any) *Error  { return newf(KindInvalid, format, args...) }
func NotFound(format string, args ...any) *Error { return newf(KindNotFound, format, args...) }
func Conflict(format string, args ...any) *Error { return newf(KindConflict, format, args...) }
func Forbidden(format string, args ...any) *Error {
	return newf(KindForbidden, format, args...)
}
func Unauthenticated(format string, args ...any) *Error {
	return newf(KindUnauthenticated, format, args...)
}
func FailedPrecondition(format string, args ...any) *Error {
	return newf(KindFailedPrecondition, format, args...)
}

// Internal wraps an unexpected error. The cause is logged, not exposed.
func Internal(err error, op string) *Error {
	return &Error{Kind: KindInternal, Message: "internal error", Err: fmt.Errorf("%s: %w", op, err)}
}

// Unavailable wraps a dependency failure.
func Unavailable(err error, op string) *Error {
	return &Error{Kind: KindUnavailable, Message: "service temporarily unavailable", Err: fmt.Errorf("%s: %w", op, err)}
}

// KindOf returns the Kind of err (KindInternal for unclassified errors).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// IsKind reports whether err is an *Error of kind k.
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}

var kindToCode = map[Kind]codes.Code{
	KindInternal:           codes.Internal,
	KindInvalid:            codes.InvalidArgument,
	KindNotFound:           codes.NotFound,
	KindConflict:           codes.AlreadyExists,
	KindUnauthenticated:    codes.Unauthenticated,
	KindForbidden:          codes.PermissionDenied,
	KindFailedPrecondition: codes.FailedPrecondition,
	KindUnavailable:        codes.Unavailable,
	KindRateLimited:        codes.ResourceExhausted,
}

// ToStatus converts any error into a gRPC status error with a safe message.
// Errors that already carry a gRPC status are passed through unchanged.
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	var e *Error
	if errors.As(err, &e) {
		return status.Error(kindToCode[e.Kind], e.Message)
	}
	return status.Error(codes.Internal, "internal error")
}

// FromStatus converts a gRPC error received from a downstream service back
// into an *Error, preserving the code's meaning and the remote message.
func FromStatus(err error, op string) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return Internal(err, op)
	}
	for k, c := range kindToCode {
		if c == st.Code() && k != KindInternal {
			return &Error{Kind: k, Message: st.Message(), Err: err}
		}
	}
	switch st.Code() {
	case codes.DeadlineExceeded, codes.Canceled:
		return Unavailable(err, op)
	}
	return Internal(err, op)
}
