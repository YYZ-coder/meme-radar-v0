// Package migrations embeds the PostgreSQL schema migrations, applied in
// lexical file-name order by store.Migrate. They are the authoritative DDL
// (docs/engineering/02-data-model.md describes them).
package migrations

import "embed"

// FS holds the *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
