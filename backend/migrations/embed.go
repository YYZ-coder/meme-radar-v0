// Package migrations embeds all SQL migration files so they can be bundled
// into the radar binary and run without a separate migrations directory on disk.
package migrations

import "embed"

// Files contains all *.sql migration files in this directory.
//
//go:embed *.sql
var Files embed.FS
