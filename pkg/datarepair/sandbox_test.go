package datarepair

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-20261016: libpq receives separate connection settings; a connection URI
// in PGDATABASE is not equivalent. Credentials must never appear in argv.
func TestSnapshotUsesLibpqEnvironment(t *testing.T) {
	t.Setenv("PGDATABASE", "unrelated-inherited-database")
	t.Setenv("PGSSLMODE", "verify-full")
	program := filepath.Join(t.TempDir(), "pg-dump-fixture")
	script := `#!/bin/sh
set -eu
test "$PGDATABASE" = 'catalog two'
test "$PGHOST" = '::1'
test "$PGPORT" = '5433'
test "$PGUSER" = 'writer'
test "$PGPASSWORD" = 'fixture-secret'
test "$PGSSLMODE" = 'disable'
case "$*" in *fixture-secret*|*postgresql://*) exit 3;; esac
printf '%s' 'synthetic snapshot'
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	r := DockerRehearser{SourceURL: "postgresql://writer:fixture-secret@[::1]:5433/catalog%20two?sslmode=disable", DumpBinary: program}
	dump, database, err := r.snapshot(t.Context())
	if err != nil || database != "catalog two" || string(dump) != "synthetic snapshot" {
		t.Fatalf("libpq connection contract failed: %q %v", database, err)
	}
}

func TestSnapshotBoundCannotBeBypassedByReaderFrom(t *testing.T) {
	output := &boundedBuffer{limit: 8}
	// Hide the reader's WriteTo method so io.Copy exercises the destination
	// fast path that os/exec uses. Embedding bytes.Buffer would bypass Write.
	source := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", 32))}
	if _, err := io.Copy(output, source); err == nil {
		t.Fatal("snapshot exceeded its bound through io.ReaderFrom")
	}
	if len(output.Bytes()) > 8 {
		t.Fatal("oversized snapshot was retained")
	}
}
