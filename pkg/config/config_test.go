package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoaderAccumulatesErrors(t *testing.T) {
	l := NewFromMap(map[string]string{
		"PORT":    "abc",
		"TIMEOUT": "5x",
		"FLAG":    "maybe",
		"SHORT":   "tooshort",
	})
	_ = l.Required("MISSING")
	_ = l.Int("PORT", 1)
	_ = l.Duration("TIMEOUT", time.Second)
	_ = l.Bool("FLAG", false)
	_ = l.Secret("SHORT", 32)
	err := l.Err()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"MISSING is required", "PORT", "TIMEOUT", "FLAG", "SHORT must be at least 32"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestLoaderValues(t *testing.T) {
	l := NewFromMap(map[string]string{"N": "7", "D": "2s", "B": "true", "L": " a, ,b ,c", "S": "  v  "})
	if l.Int("N", 0) != 7 || l.Duration("D", 0) != 2*time.Second || !l.Bool("B", false) || l.String("S", "") != "v" {
		t.Fatal("unexpected parsed values")
	}
	if got := l.List("L", nil); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("List = %v", got)
	}
	if l.String("UNSET", "def") != "def" || l.Int("UNSET", 3) != 3 {
		t.Fatal("defaults not applied")
	}
	if err := l.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCommonRequiresStrongSecret(t *testing.T) {
	l := NewFromMap(map[string]string{})
	l.LoadCommon("svc")
	if l.Err() == nil || !strings.Contains(l.Err().Error(), "JWT_SECRET") {
		t.Fatalf("missing JWT_SECRET must fail, got %v", l.Err())
	}
}

func TestPlaceholderSecretRejectedInProduction(t *testing.T) {
	l := NewFromMap(map[string]string{
		"APP_ENV":    "production",
		"JWT_SECRET": "change-me-to-a-long-random-string-of-at-least-32-chars",
	})
	l.LoadCommon("svc")
	if l.Err() == nil {
		t.Fatal("placeholder secret must be rejected in production")
	}
}
