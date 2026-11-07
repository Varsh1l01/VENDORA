// Package requestid carries a correlation id across HTTP, gRPC and Kafka.
package requestid

import (
	"context"

	"github.com/google/uuid"
)

// Header is the HTTP header and (lower-cased) gRPC metadata key.
const Header = "X-Request-ID"

// MetadataKey is the gRPC metadata / Kafka header key.
const MetadataKey = "x-request-id"

type ctxKey struct{}

// With stores id in ctx.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the request id in ctx, or "".
func From(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// New returns a fresh request id.
func New() string { return uuid.NewString() }

// Sanitize returns id when it looks safe to propagate, otherwise a new id.
func Sanitize(id string) string {
	if id == "" || len(id) > 128 {
		return New()
	}
	for _, r := range id {
		allowed := r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !allowed {
			return New()
		}
	}
	return id
}
