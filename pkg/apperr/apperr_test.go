package apperr

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusMapsKindsAndHidesInternals(t *testing.T) {
	tests := []struct {
		err  error
		code codes.Code
		msg  string
	}{
		{Invalid("bad %s", "x"), codes.InvalidArgument, "bad x"},
		{NotFound("nope"), codes.NotFound, "nope"},
		{Conflict("dup"), codes.AlreadyExists, "dup"},
		{Forbidden("no"), codes.PermissionDenied, "no"},
		{Unauthenticated("who"), codes.Unauthenticated, "who"},
		{FailedPrecondition("state"), codes.FailedPrecondition, "state"},
		{Internal(errors.New("pq: password=secret"), "op"), codes.Internal, "internal error"},
		{Unavailable(errors.New("dial tcp"), "op"), codes.Unavailable, "service temporarily unavailable"},
		{errors.New("raw driver error with secrets"), codes.Internal, "internal error"},
		{fmt.Errorf("wrapped: %w", NotFound("inner")), codes.NotFound, "inner"},
	}
	for _, tt := range tests {
		st := status.Convert(ToStatus(tt.err))
		if st.Code() != tt.code || st.Message() != tt.msg {
			t.Errorf("ToStatus(%v) = (%v, %q), want (%v, %q)", tt.err, st.Code(), st.Message(), tt.code, tt.msg)
		}
	}
	if ToStatus(nil) != nil {
		t.Error("ToStatus(nil) must be nil")
	}
}

func TestFromStatusRoundTrip(t *testing.T) {
	for _, k := range []Kind{KindInvalid, KindNotFound, KindConflict, KindForbidden, KindFailedPrecondition, KindUnavailable} {
		remote := ToStatus(&Error{Kind: k, Message: "m"})
		got := FromStatus(remote, "op")
		if KindOf(got) != k {
			t.Errorf("kind %v round-tripped to %v", k, KindOf(got))
		}
	}
	if KindOf(FromStatus(status.Error(codes.DeadlineExceeded, "slow"), "op")) != KindUnavailable {
		t.Error("deadline exceeded must map to Unavailable")
	}
	if FromStatus(nil, "op") != nil {
		t.Error("FromStatus(nil) must be nil")
	}
}

func TestErrorsIsByKind(t *testing.T) {
	err := fmt.Errorf("ctx: %w", NotFound("x"))
	if !errors.Is(err, &Error{Kind: KindNotFound}) {
		t.Fatal("errors.Is should match on kind")
	}
	if errors.Is(err, &Error{Kind: KindConflict}) {
		t.Fatal("errors.Is must not match a different kind")
	}
}
