// Package pagination encodes opaque keyset cursors of the form
// (created_at, id), matching ORDER BY created_at DESC, id DESC.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/Varsh1l01/VENDORA/pkg/apperr"
)

// Cursor marks the last row of a page.
type Cursor struct {
	CreatedAt time.Time `json:"t"`
	ID        string    `json:"id"`
}

// Encode returns the opaque token for c.
func Encode(c Cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses a token. An empty token yields (nil, nil): the first page.
func Decode(token string) (*Cursor, error) {
	if token == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) > 256 {
		return nil, apperr.Invalid("page_token is invalid")
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil || c.ID == "" || c.CreatedAt.IsZero() {
		return nil, apperr.Invalid("page_token is invalid")
	}
	return &c, nil
}
