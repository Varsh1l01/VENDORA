package pagination

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	c := Cursor{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC), ID: "550e8400-e29b-41d4-a716-446655440000"}
	got, err := Decode(Encode(c))
	if err != nil || got == nil || !got.CreatedAt.Equal(c.CreatedAt) || got.ID != c.ID {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestDecode(t *testing.T) {
	if c, err := Decode(""); c != nil || err != nil {
		t.Fatal("empty token must mean first page")
	}
	for _, tok := range []string{"%%%", "bm90LWpzb24", "e30"} { // invalid b64, "not-json", "{}"
		if _, err := Decode(tok); err == nil {
			t.Errorf("Decode(%q) should fail", tok)
		}
	}
}
