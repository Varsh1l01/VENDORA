package migrations

import "embed"

// FS holds this service's SQL migrations, applied at startup.
//
//go:embed *.sql
var FS embed.FS
