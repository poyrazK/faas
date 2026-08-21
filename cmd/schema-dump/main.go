// Command schema-dump regenerates schema.sql from a live Postgres for
// sqlc to consume. The Makefile's schema-dump target used to be a shell
// recipe (psql preflight + go run ./cmd/migrate + pg_dump -s + sed
// noise filter); this binary owns the same flow in Go so the failure
// modes live behind os/exec and the regex noise filter is
// compile-checked and unit-testable.
//
// schema.sql is the single source-of-truth schema file sqlc consumes
// (issue #125, ADR-017). sqlc v1.27.0 does not merge
// `create table if not exists` statements across multiple migration
// files, so pointing sqlc at migrations/ would diverge from the live
// schema wherever a migration adds columns to an existing table.
// Instead, schema-dump applies the full migration set against a
// reachable Postgres, runs pg_dump -s, strips the version-noise lines
// pg_dump emits (which can change with every Postgres minor version),
// and writes the deterministic output to schema.sql. The sqlc-check
// CI gate then diffs the regenerated pkg/state/sqlc/* against the
// committed baseline, which transitively proves schema.sql is in sync
// with the migration set.
//
// Usage:
//
//	DATABASE_URL=postgres://... schema-dump           # writes ./schema.sql
//	DATABASE_URL=postgres://... schema-dump -o other   # writes ./other
//
// Exit code 0 on success, 1 on any failure. Failures print to stderr
// with the failing operation's name so CI surfaces the diagnostic
// cleanly. Idempotent: re-running against the same migration set
// produces byte-identical output (verified by the deterministic
// pg_dump -s output against an unchanged schema).
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
)

const defaultOutput = "schema.sql"

// pgDumpNoise are pg_dump -s banner lines that change between
// Postgres minor versions and would defeat a textual diff. Compiled
// once at init; missing a new noise line is loud (a stale
// schema.sql in CI), which is the failure mode we want — never
// silent drift detection loss. Each pattern matches the full line
// (including the trailing newline); (?m) makes ^ match each line
// start, not the input start, so a banner that appears mid-file is
// also caught.
var pgDumpNoise = []*regexp.Regexp{
	// \restrict <token> and \unrestrict <token> are psql meta-commands
	// pg_dump 16+ emits at the top of the dump. The token is random
	// per dump, so a textual diff would always fail without this.
	regexp.MustCompile(`(?m)^\\(restrict|unrestrict) [^\n]*\n?`),
	regexp.MustCompile(`(?m)^-- Dumped from database version [^\n]*\n?`),
	regexp.MustCompile(`(?m)^-- Dumped by pg_dump version [^\n]*\n?`),
	regexp.MustCompile(`(?m)^-- PostgreSQL database dump complete[^\n]*\n?`),
}

func main() {
	outPath := flag.String("o", defaultOutput, "output path (default ./schema.sql)")
	flag.Parse()

	if err := run(*outPath, liveRunner{}); err != nil {
		fmt.Fprintf(os.Stderr, "schema-dump: %v\n", err)
		os.Exit(1)
	}
}

// runner abstracts the I/O live `run` does against a real Postgres +
// pg_dump. Tests inject a fakeRunner to assert error paths
// (DATABASE_URL missing, pg_dump failure, db.Open failure) without
// requiring live infra. Exposed at package scope for testability
// (cmd/schema-dump/main_test.go).
type runner interface {
	// envLookup is os.Getenv in production; tests return a fixed
	// value to assert the DATABASE_URL guard.
	envLookup(key string) string
	// pgDump shells out to pg_dump -s in production; tests return
	// canned bytes (or an error) to assert the pg_dump error path
	// and the stripNoise pass without a live Postgres.
	pgDump(ctx context.Context, dsn string) ([]byte, error)
	// openPool wraps db.Open + db.MigrateUp so tests can return a
	// pre-built pool or a synthetic error. The returned closer's
	// Close() is called by run on success AND on the pg_dump path
	// (deferred); on error, openPool returns nil and Close() is not
	// called.
	openPool(ctx context.Context) (poolCloser, error)
}

// poolCloser is the subset of *pgxpool.Pool.Close() run needs. The
// pgxpool.Pool type's Close() has no error return so an interface is
// the cleanest way to wrap it for tests without exposing the full
// pool surface.
type poolCloser interface {
	Close()
}

// liveRunner is the production wiring for `run`. Calls os.Getenv,
// exec.CommandContext("pg_dump", ...), and db.Open/db.MigrateUp.
type liveRunner struct{}

func (liveRunner) envLookup(key string) string { return os.Getenv(key) }

func (liveRunner) pgDump(ctx context.Context, dsn string) ([]byte, error) {
	return exec.CommandContext(ctx, "pg_dump",
		"-s", "--no-owner", "--no-privileges",
		"--no-sync", "--no-tablespaces", dsn,
	).Output()
}

func (liveRunner) openPool(ctx context.Context) (poolCloser, error) {
	pool, err := db.Open(ctx, "")
	if err != nil {
		return nil, err
	}
	// F2 / ADR-124 / PR-2 audit: schema-dump always applies migrations
	// before pg_dump so the dump reflects HEAD; this acquires the lock
	// just like every daemon. The fast no-migration path skips this
	// call via db.Status() in cmd/migrate -status (different binary).
	if err := db.MigrateUp(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func run(outPath string, r runner) error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. Apply migrations so the live schema matches HEAD.
	pool, err := r.openPool(ctx)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer pool.Close()
	log.Info("migrations applied")

	// 2. Shell to pg_dump -s. We don't reimplement the schema-dump
	// renderer in Go because the canonical format is pg_dump's, and
	// every Postgres minor version can change it. Failure modes
	// (pg_dump missing, DSN invalid) are owned by os/exec.
	dsn := r.envLookup("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}
	dump, err := r.pgDump(ctx, dsn)
	if err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}

	// 3. Strip pg_dump version noise so the diff is stable across
	// Postgres minor versions.
	filtered := stripNoise(dump)

	if err := os.WriteFile(outPath, filtered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	lines := bytes.Count(filtered, []byte{'\n'}) + 1
	fmt.Fprintf(os.Stderr, "schema-dump: %s regenerated (%d lines)\n", outPath, lines)
	return nil
}

// stripNoise removes the pg_dump -s banner lines that change between
// Postgres minor versions. Exposed at package scope for testability
// (cmd/schema-dump/main_test.go::TestStripNoise).
func stripNoise(in []byte) []byte {
	out := in
	for _, re := range pgDumpNoise {
		out = re.ReplaceAll(out, nil)
	}
	return out
}
