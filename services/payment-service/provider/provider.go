// Package provider abstracts the external payment service provider (PSP).
//
// The Fake provider is the default: it lets the whole checkout run locally
// and in tests. A real adapter (e.g. Stripe) implements the same interface
// and must forward IdempotencyKey to the PSP so retries never double-charge.
package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ChargeRequest is a request to capture funds.
type ChargeRequest struct {
	IdempotencyKey string // stable per payment; retries must reuse it
	AmountCents    int64
	Currency       string
	Token          string // payment method token from the client
}

// ChargeResult is the PSP's answer.
type ChargeResult struct {
	Reference string
	Declined  bool
	Reason    string
}

// ErrTransient signals a retryable PSP failure (timeout, 5xx).
var ErrTransient = errors.New("payment provider temporarily unavailable")

// Provider charges and refunds.
type Provider interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
	Refund(ctx context.Context, reference string, amountCents int64) error
}

// Test tokens understood by Fake.
const (
	TokenDecline = "tok_decline" // card declined
	TokenError   = "tok_error"   // transient provider error
)

// Fake is an in-memory PSP that honours idempotency keys.
type Fake struct {
	Latency time.Duration

	mu      sync.Mutex
	charges map[string]ChargeResult
}

// NewFake returns a Fake provider.
func NewFake(latency time.Duration) *Fake {
	return &Fake{Latency: latency, charges: map[string]ChargeResult{}}
}

// Charge implements Provider.
func (f *Fake) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	select {
	case <-ctx.Done():
		return ChargeResult{}, ctx.Err()
	case <-time.After(f.Latency):
	}
	if req.Token == TokenError {
		return ChargeResult{}, ErrTransient
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.charges[req.IdempotencyKey]; ok {
		return r, nil // idempotent replay
	}
	r := ChargeResult{Reference: "fake_ch_" + uuid.NewString()}
	if req.Token == TokenDecline {
		r = ChargeResult{Declined: true, Reason: "card declined"}
	}
	f.charges[req.IdempotencyKey] = r
	return r, nil
}

// Refund implements Provider.
func (f *Fake) Refund(ctx context.Context, _ string, _ int64) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.Latency):
		return nil
	}
}
