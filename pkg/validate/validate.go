// Package validate contains input validation shared by the gateway and the
// services. Services validate again (defense in depth) because they can be
// called by other services, not only through the gateway.
package validate

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

// Limits.
const (
	MaxQuantityPerLine = 99
	MaxCartLines       = 50
	MaxPriceCents      = 100_000_000 // 1,000,000.00
	MaxStock           = 1_000_000
	MinPasswordLen     = 8
	MaxPasswordLen     = 72 // bcrypt limit
	MaxPageSize        = 100
	DefaultPageSize    = 20
)

// Email validates and normalizes an email address (lower-case, trimmed).
// Addresses containing CR/LF or display names are rejected, which prevents
// SMTP header injection downstream.
func Email(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n<>\" ,;") {
		return "", apperr.Invalid("email is invalid")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" || !strings.Contains(s[strings.LastIndexByte(s, '@')+1:], ".") {
		return "", apperr.Invalid("email is invalid")
	}
	return s, nil
}

// Password enforces length bounds.
func Password(p string) error {
	if len(p) < MinPasswordLen || len(p) > MaxPasswordLen {
		return apperr.Invalid("password must be between %d and %d bytes", MinPasswordLen, MaxPasswordLen)
	}
	return nil
}

// Text trims s and checks its length in runes is within [min, max].
func Text(field, s string, min, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		if min > 0 && n == 0 {
			return "", apperr.Invalid("%s is required", field)
		}
		return "", apperr.Invalid("%s must be between %d and %d characters", field, min, max)
	}
	if strings.ContainsAny(s, "\x00") {
		return "", apperr.Invalid("%s contains invalid characters", field)
	}
	return s, nil
}

// ID checks that s is a canonical UUID.
func ID(field, s string) error {
	u, err := uuid.Parse(s)
	if err != nil || u.String() != strings.ToLower(s) {
		return apperr.Invalid("%s must be a valid UUID", field)
	}
	return nil
}

// Quantity checks 1 <= q <= MaxQuantityPerLine.
func Quantity(q int32) error {
	if q < 1 || q > MaxQuantityPerLine {
		return apperr.Invalid("quantity must be between 1 and %d", MaxQuantityPerLine)
	}
	return nil
}

// PriceCents checks 0 <= p <= MaxPriceCents.
func PriceCents(p int64) error {
	if p < 0 || p > MaxPriceCents {
		return apperr.Invalid("price_cents must be between 0 and %d", MaxPriceCents)
	}
	return nil
}

// Stock checks 0 <= s <= MaxStock.
func Stock(s int32) error {
	if s < 0 || s > MaxStock {
		return apperr.Invalid("stock must be between 0 and %d", MaxStock)
	}
	return nil
}

// URL accepts an empty string or an http(s) URL up to 2048 characters.
func URL(field, s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	httpScheme := strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
	if len(s) > 2048 || !httpScheme || strings.ContainsAny(s, " \r\n\"<>") {
		return "", apperr.Invalid("%s must be an http(s) URL", field)
	}
	return s, nil
}

// PageSize clamps n to [1, MaxPageSize], defaulting to DefaultPageSize.
func PageSize(n int32) int32 {
	switch {
	case n <= 0:
		return DefaultPageSize
	case n > MaxPageSize:
		return MaxPageSize
	default:
		return n
	}
}
