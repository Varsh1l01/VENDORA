// Package auth issues and verifies VENDORA access tokens and models the
// authenticated caller (Principal) used for authorization decisions.
//
// Tokens are HS256 JWTs with pinned algorithm, issuer and audience. Two kinds
// of principals exist:
//   - users (role "customer" or "admin"), authenticated by the gateway and
//     forwarded to services so ownership is enforced where data lives;
//   - services (role "service"), minted by a service for its own outbound
//     calls, which may act on behalf of any user.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

// Roles.
const (
	RoleCustomer = "customer"
	RoleAdmin    = "admin"
	RoleService  = "service"
)

// Principal is the authenticated caller.
type Principal struct {
	Subject string // user id, or service name for service principals
	Email   string
	Role    string
}

// IsService reports whether the caller is an internal service.
func (p Principal) IsService() bool { return p.Role == RoleService }

// IsAdmin reports whether the caller is an administrator.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

// Claims are the JWT claims used by VENDORA.
type Claims struct {
	Email string `json:"email,omitempty"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

// Tokens issues and verifies tokens.
type Tokens struct {
	secret   []byte
	issuer   string
	audience string
	now      func() time.Time
}

// NewTokens returns a token issuer/verifier.
func NewTokens(secret, issuer, audience string) *Tokens {
	return &Tokens{secret: []byte(secret), issuer: issuer, audience: audience, now: time.Now}
}

// WithClock overrides the clock (tests).
func (t *Tokens) WithClock(now func() time.Time) *Tokens { t.now = now; return t }

// Issue signs a token for p valid for ttl and returns it with its expiry.
func (t *Tokens) Issue(p Principal, ttl time.Duration) (string, time.Time, error) {
	now := t.now()
	exp := now.Add(ttl)
	claims := Claims{
		Email: p.Email,
		Role:  p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   p.Subject,
			Issuer:    t.issuer,
			Audience:  jwt.ClaimStrings{t.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        uuid.NewString(),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return s, exp, nil
}

// Verify parses and validates a token string.
func (t *Tokens) Verify(token string) (Principal, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		return Principal{}, apperr.Unauthenticated("invalid or expired token")
	}
	if c.Subject == "" || (c.Role != RoleCustomer && c.Role != RoleAdmin && c.Role != RoleService) {
		return Principal{}, apperr.Unauthenticated("invalid token claims")
	}
	return Principal{Subject: c.Subject, Email: c.Email, Role: c.Role}, nil
}

type ctxKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal stored in ctx.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// ErrNoPrincipal is returned when an operation requires an authenticated caller.
var ErrNoPrincipal = errors.New("no principal in context")

// ResolveUser returns the user id an operation should act on.
//
// Services may act on behalf of any user (requestedUserID must be set).
// Admins may act on any user; when requestedUserID is empty they act on
// themselves. Customers may only act on themselves: a mismatching
// requestedUserID is rejected.
func ResolveUser(ctx context.Context, requestedUserID string) (string, error) {
	p, ok := FromContext(ctx)
	if !ok {
		return "", apperr.Unauthenticated("authentication required")
	}
	switch {
	case p.IsService():
		if requestedUserID == "" {
			return "", apperr.Invalid("user_id is required")
		}
		return requestedUserID, nil
	case p.IsAdmin():
		if requestedUserID == "" {
			return p.Subject, nil
		}
		return requestedUserID, nil
	default:
		if requestedUserID != "" && requestedUserID != p.Subject {
			return "", apperr.Forbidden("cannot act on another user's resources")
		}
		return p.Subject, nil
	}
}

// CanAccessOwnedBy reports whether the caller may read a resource owned by
// ownerID. Used to return NotFound (not Forbidden) for other users' data so
// resource existence is not leaked.
func CanAccessOwnedBy(ctx context.Context, ownerID string) bool {
	p, ok := FromContext(ctx)
	if !ok {
		return false
	}
	return p.IsService() || p.IsAdmin() || p.Subject == ownerID
}
