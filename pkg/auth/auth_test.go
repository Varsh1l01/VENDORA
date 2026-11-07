package auth

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

const secret = "test-secret-that-is-long-enough-1234567890"

func newTokens() *Tokens { return NewTokens(secret, "vendora-test", "vendora") }

func TestIssueVerifyRoundTrip(t *testing.T) {
	tk := newTokens()
	tok, exp, err := tk.Issue(Principal{Subject: "u1", Email: "a@b.co", Role: RoleCustomer}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) <= 0 {
		t.Fatalf("expiry in the past: %v", exp)
	}
	p, err := tk.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "u1" || p.Role != RoleCustomer || p.Email != "a@b.co" {
		t.Fatalf("unexpected principal %+v", p)
	}
}

func TestVerifyRejects(t *testing.T) {
	good := newTokens()
	now := time.Now()

	sign := func(method jwt.SigningMethod, key any, c Claims) string {
		s, err := jwt.NewWithClaims(method, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := func() Claims {
		return Claims{Role: RoleCustomer, RegisteredClaims: jwt.RegisteredClaims{
			Subject: "u1", Issuer: "vendora-test", Audience: jwt.ClaimStrings{"vendora"},
			IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		}}
	}

	expired := base()
	expired.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Hour))
	wrongAud := base()
	wrongAud.Audience = jwt.ClaimStrings{"someone-else"}
	wrongIss := base()
	wrongIss.Issuer = "evil"
	badRole := base()
	badRole.Role = "superuser"
	noExp := base()
	noExp.ExpiresAt = nil
	noSub := base()
	noSub.Subject = ""

	cases := map[string]string{
		"garbage":         "not-a-token",
		"wrong secret":    sign(jwt.SigningMethodHS256, []byte("another-secret-that-is-long-enough-123"), base()),
		"alg none":        sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, base()),
		"alg HS512":       sign(jwt.SigningMethodHS512, []byte(secret), base()),
		"expired":         sign(jwt.SigningMethodHS256, []byte(secret), expired),
		"wrong audience":  sign(jwt.SigningMethodHS256, []byte(secret), wrongAud),
		"wrong issuer":    sign(jwt.SigningMethodHS256, []byte(secret), wrongIss),
		"unknown role":    sign(jwt.SigningMethodHS256, []byte(secret), badRole),
		"missing exp":     sign(jwt.SigningMethodHS256, []byte(secret), noExp),
		"missing subject": sign(jwt.SigningMethodHS256, []byte(secret), noSub),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := good.Verify(tok); !apperr.IsKind(err, apperr.KindUnauthenticated) {
				t.Fatalf("expected Unauthenticated, got %v", err)
			}
		})
	}
}

func TestResolveUser(t *testing.T) {
	cust := WithPrincipal(context.Background(), Principal{Subject: "u1", Role: RoleCustomer})
	adm := WithPrincipal(context.Background(), Principal{Subject: "a1", Role: RoleAdmin})
	svc := WithPrincipal(context.Background(), Principal{Subject: "order-service", Role: RoleService})

	tests := []struct {
		name    string
		ctx     context.Context
		req     string
		want    string
		errKind apperr.Kind
		wantErr bool
	}{
		{"anonymous", context.Background(), "", "", apperr.KindUnauthenticated, true},
		{"customer self implicit", cust, "", "u1", 0, false},
		{"customer self explicit", cust, "u1", "u1", 0, false},
		{"customer other user", cust, "u2", "", apperr.KindForbidden, true},
		{"admin implicit self", adm, "", "a1", 0, false},
		{"admin other user", adm, "u2", "u2", 0, false},
		{"service needs user", svc, "", "", apperr.KindInvalid, true},
		{"service on behalf", svc, "u2", "u2", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveUser(tt.ctx, tt.req)
			if tt.wantErr {
				if !apperr.IsKind(err, tt.errKind) {
					t.Fatalf("want kind %v, got %v", tt.errKind, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got (%q, %v), want %q", got, err, tt.want)
			}
		})
	}
}

func TestCanAccessOwnedBy(t *testing.T) {
	cust := WithPrincipal(context.Background(), Principal{Subject: "u1", Role: RoleCustomer})
	if !CanAccessOwnedBy(cust, "u1") || CanAccessOwnedBy(cust, "u2") {
		t.Fatal("customer must access only own resources")
	}
	if CanAccessOwnedBy(context.Background(), "u1") {
		t.Fatal("anonymous must not access owned resources")
	}
	adm := WithPrincipal(context.Background(), Principal{Subject: "a", Role: RoleAdmin})
	if !CanAccessOwnedBy(adm, "u2") {
		t.Fatal("admin must access any resource")
	}
}
