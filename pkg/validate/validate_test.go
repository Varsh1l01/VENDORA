package validate

import (
	"strings"
	"testing"
)

func TestEmail(t *testing.T) {
	ok := map[string]string{
		"User@Example.com":     "user@example.com",
		"  a.b+tag@shop.io  ":  "a.b+tag@shop.io",
		"x@sub.domain.example": "x@sub.domain.example",
	}
	for in, want := range ok {
		got, err := Email(in)
		if err != nil || got != want {
			t.Errorf("Email(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "plain", "a@b", "a@@b.com", "Name <a@b.com>",
		"a@b.com\r\nBcc: victim@x.com", // header injection
		"a@b.com\nX: y", "a b@c.com", strings.Repeat("a", 250) + "@b.com",
	}
	for _, in := range bad {
		if _, err := Email(in); err == nil {
			t.Errorf("Email(%q) should fail", in)
		}
	}
}

func TestQuantityAndPrice(t *testing.T) {
	for _, q := range []int32{-5, 0, 100} {
		if Quantity(q) == nil {
			t.Errorf("Quantity(%d) should fail", q)
		}
	}
	for _, q := range []int32{1, 99} {
		if err := Quantity(q); err != nil {
			t.Errorf("Quantity(%d): %v", q, err)
		}
	}
	if PriceCents(-1) == nil || PriceCents(MaxPriceCents+1) == nil {
		t.Error("PriceCents bounds not enforced")
	}
	if PriceCents(0) != nil || PriceCents(1999) != nil {
		t.Error("valid prices rejected")
	}
}

func TestID(t *testing.T) {
	if ID("id", "550e8400-e29b-41d4-a716-446655440000") != nil {
		t.Error("valid UUID rejected")
	}
	for _, s := range []string{"", "123", "550e8400e29b41d4a716446655440000", "' OR 1=1 --"} {
		if ID("id", s) == nil {
			t.Errorf("ID(%q) should fail", s)
		}
	}
}

func TestText(t *testing.T) {
	if _, err := Text("name", "   ", 1, 10); err == nil {
		t.Error("blank required text accepted")
	}
	if v, err := Text("name", "  Ada  ", 1, 10); err != nil || v != "Ada" {
		t.Errorf("got %q %v", v, err)
	}
	if _, err := Text("name", "héllo wörld!", 1, 5); err == nil {
		t.Error("too long accepted")
	}
	if _, err := Text("name", "a\x00b", 1, 10); err == nil {
		t.Error("NUL byte accepted")
	}
}

func TestPageSize(t *testing.T) {
	cases := map[int32]int32{-1: DefaultPageSize, 0: DefaultPageSize, 1: 1, 50: 50, 100: 100, 1000: MaxPageSize}
	for in, want := range cases {
		if got := PageSize(in); got != want {
			t.Errorf("PageSize(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestURL(t *testing.T) {
	if v, err := URL("u", ""); err != nil || v != "" {
		t.Error("empty URL should be allowed")
	}
	if _, err := URL("u", "https://cdn.example.com/a.png"); err != nil {
		t.Error(err)
	}
	for _, s := range []string{"javascript:alert(1)", "ftp://x", "https://a b", "https://x\"onerror"} {
		if _, err := URL("u", s); err == nil {
			t.Errorf("URL(%q) should fail", s)
		}
	}
}
