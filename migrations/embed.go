// Package migrations re-exports the migration SQL files as an embed.FS so
// pkg/db can apply them on startup without depending on the host filesystem.
// Keep this file in package `migrations` (the package name matches the
// directory) so `//go:embed *.sql` picks up 00001_init.sql + 00002_*.sql
// added later.
package migrations

import "embed"

// FS is the embedded migration set. pkg/db.MigrateUp calls goose.SetBaseFS
// against this.
//
//go:embed *.sql
var FS embed.FS
