// Package config loads service configuration from environment variables.
//
// Loading is explicit and fail-fast: a Loader accumulates every problem
// (missing required values, unparsable numbers, weak secrets) and Err reports
// them all at once, so a misconfigured service refuses to start instead of
// silently running with a fallback.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Loader reads typed values from the environment and records errors.
type Loader struct {
	lookup func(string) (string, bool)
	errs   []error
}

// New returns a Loader backed by the process environment.
func New() *Loader { return &Loader{lookup: os.LookupEnv} }

// NewFromMap returns a Loader backed by a map (useful in tests).
func NewFromMap(m map[string]string) *Loader {
	return &Loader{lookup: func(k string) (string, bool) { v, ok := m[k]; return v, ok }}
}

func (l *Loader) raw(key string) (string, bool) {
	v, ok := l.lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

// String returns the value of key or def when unset.
func (l *Loader) String(key, def string) string {
	if v, ok := l.raw(key); ok {
		return v
	}
	return def
}

// Required returns the value of key and records an error when it is unset.
func (l *Loader) Required(key string) string {
	v, ok := l.raw(key)
	if !ok {
		l.errs = append(l.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

// Secret is like Required but also enforces a minimum length.
func (l *Loader) Secret(key string, minLen int) string {
	v := l.Required(key)
	if v != "" && len(v) < minLen {
		l.errs = append(l.errs, fmt.Errorf("%s must be at least %d characters", key, minLen))
	}
	return v
}

// Int returns key parsed as int, or def when unset.
func (l *Loader) Int(key string, def int) int {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid integer %q", key, v))
		return def
	}
	return n
}

// Bool returns key parsed as bool, or def when unset.
func (l *Loader) Bool(key string, def bool) bool {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid boolean %q", key, v))
		return def
	}
	return b
}

// Duration returns key parsed with time.ParseDuration, or def when unset.
func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q", key, v))
		return def
	}
	return d
}

// List returns key split on commas with empty items removed, or def.
func (l *Loader) List(key string, def []string) []string {
	v, ok := l.raw(key)
	if !ok {
		return def
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Err returns all accumulated configuration errors, or nil.
func (l *Loader) Err() error { return errors.Join(l.errs...) }

// Common holds settings shared by every service.
type Common struct {
	ServiceName string
	Env         string
	LogLevel    string
	AdminAddr   string
	JWTSecret   string
	JWTIssuer   string
	JWTAudience string
	ShutdownTTL time.Duration
}

// LoadCommon reads the settings shared by all services.
func (l *Loader) LoadCommon(service string) Common {
	c := Common{
		ServiceName: service,
		Env:         l.String("APP_ENV", "development"),
		LogLevel:    l.String("LOG_LEVEL", "info"),
		AdminAddr:   l.String("ADMIN_ADDR", ":9090"),
		JWTSecret:   l.Secret("JWT_SECRET", 32),
		JWTIssuer:   l.String("JWT_ISSUER", "vendora-user-service"),
		JWTAudience: l.String("JWT_AUDIENCE", "vendora"),
		ShutdownTTL: l.Duration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}
	if c.Env == "production" && strings.Contains(strings.ToLower(c.JWTSecret), "change-me") {
		l.errs = append(l.errs, errors.New("JWT_SECRET still contains the .env.example placeholder"))
	}
	return c
}
