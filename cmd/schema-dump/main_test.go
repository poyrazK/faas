package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrationDSNUTC(t *testing.T) {
	t.Setenv("PGTZ", "Europe/Istanbul")
	for _, dsn := range []string{
		"postgres://owner@localhost/reviewed?sslmode=disable",
		"postgresql://owner@/reviewed?host=/local/socket&timezone=Europe/Istanbul",
		"user=owner host=/local/socket dbname=reviewed timezone=Europe/Istanbul",
	} {
		t.Run(dsn, func(t *testing.T) {
			canonical, err := migrationDSNUTC(dsn)
			if err != nil {
				t.Fatal(err)
			}
			before, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			after, err := pgx.ParseConfig(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if after.RuntimeParams["timezone"] != "UTC" {
				t.Fatal("migration sessions inherit a non-UTC timezone")
			}
			if after.Host != before.Host || after.Database != before.Database || after.User != before.User || after.Port != before.Port {
				t.Fatal("timezone selection changed the target database")
			}
		})
	}
}

func TestLivePgDumpPinsUTC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("#!/bin/sh\nprintf '%s' \"$PGTZ\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PGTZ", "Europe/Istanbul")
	got, err := (liveRunner{}).pgDump(t.Context(), "postgres:///reviewed")
	if err != nil || string(got) != "UTC" {
		t.Fatalf("pg_dump timezone = %q, err = %v; want UTC", got, err)
	}
}

func TestStripNoise(t *testing.T) {
	in := strings.Join([]string{
		"\\restrict abc123",
		"-- Dumped from database version 16.2",
		"-- Dumped by pg_dump version 16.2",
		"CREATE TABLE public.crons (",
		"    id uuid NOT NULL,",
		");",
		"-- PostgreSQL database dump complete",
		"",
	}, "\n")

	got := string(stripNoise([]byte(in)))
	for _, banned := range []string{
		"\\restrict",
		"Dumped from database version",
		"Dumped by pg_dump version",
		"PostgreSQL database dump complete",
	} {
		if strings.Contains(got, banned) {
			t.Errorf("stripNoise left %q in output:\n%s", banned, got)
		}
	}
	if !strings.Contains(got, "CREATE TABLE public.crons") {
		t.Errorf("stripNoise removed the real schema content:\n%s", got)
	}
}

// TestStripNoise_PreservesUnrelated ensures the noise filter is
// surgical: a `-- PostgreSQL` comment in a function body must survive.
func TestStripNoise_PreservesUnrelated(t *testing.T) {
	in := "-- PostgreSQL is the database\n-- Dumped by pg_dump version 16.2\n"
	got := string(stripNoise([]byte(in)))
	if !strings.Contains(got, "-- PostgreSQL is the database") {
		t.Errorf("stripNoise over-filtered; lost the unrelated -- PostgreSQL comment:\n%s", got)
	}
	if strings.Contains(got, "Dumped by pg_dump") {
		t.Errorf("stripNoise left pg_dump version line in output:\n%s", got)
	}
}

// fakeRunner returns canned bytes / errors so the run() error paths
// can be asserted without a live Postgres. The poolCloser returned by
// openPool is a no-op so defer Close() is safe.
type fakeRunner struct {
	env        string
	dump       []byte
	dumpErr    error
	poolErr    error
	closeCount int
}

func (f *fakeRunner) envLookup(string) string { return f.env }

func (f *fakeRunner) pgDump(ctx context.Context, _ string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return f.dump, f.dumpErr
}

func (f *fakeRunner) openPool(ctx context.Context) (poolCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.poolErr != nil {
		return nil, f.poolErr
	}
	return fakeCloser{f: f}, nil
}

type fakeCloser struct{ f *fakeRunner }

func (c fakeCloser) Close() { c.f.closeCount++ }

// TestRun_DatabaseURLMissing is the regression for the explicit guard
// at the pg_dump call site — a missing DATABASE_URL must surface as a
// clear error rather than silently writing an empty schema.sql.
func TestRun_DatabaseURLMissing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "schema.sql")
	f := &fakeRunner{env: ""}

	err := run(out, f)
	if err == nil {
		t.Fatal("run succeeded with empty DATABASE_URL; want error")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL not set") {
		t.Errorf("error = %q, want DATABASE_URL not set", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("schema.sql was written despite DATABASE_URL missing")
	}
	// The pool was opened successfully; run defers pool.Close() so it
	// is still released. closeCount=1 proves the defer ran.
	if f.closeCount != 1 {
		t.Errorf("pool close count = %d, want 1 (defer should run on error path)", f.closeCount)
	}
}

// TestRun_PgDumpError is the regression for the pg_dump exec error
// path — `make schema-dump` against an unreachable Postgres or a
// missing pg_dump binary surfaces as a clear wrap of the underlying
// error so CI shows the diagnostic, not a silent empty schema.sql.
func TestRun_PgDumpError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "schema.sql")
	wantErr := errors.New("pg_dump binary not found")
	f := &fakeRunner{env: "postgres://...", dumpErr: wantErr}

	err := run(out, f)
	if err == nil {
		t.Fatal("run succeeded with pg_dump error; want error")
	}
	if !strings.Contains(err.Error(), "pg_dump:") || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Errorf("error = %q, want wrap of pg_dump with %q", err, wantErr)
	}
}

// TestRun_OpenPoolError is the regression for db.Open / db.MigrateUp
// failures (wrong DSN, unreachable Postgres, missing migration files).
// Without this, a Postgres outage would surface as an opaque "open"
// wrap with no indication of which step failed.
func TestRun_OpenPoolError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "schema.sql")
	wantErr := errors.New("connection refused")
	f := &fakeRunner{poolErr: wantErr}

	err := run(out, f)
	if err == nil {
		t.Fatal("run succeeded with openPool error; want error")
	}
	if !strings.Contains(err.Error(), "open:") || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Errorf("error = %q, want wrap of open with %q", err, wantErr)
	}
	if f.closeCount != 0 {
		t.Errorf("openPool returned a pool on error path; closeCount=%d", f.closeCount)
	}
}

// TestRun_HappyPath strips noise and writes the file. The
// fakeRunner returns canned pg_dump bytes; the test asserts both that
// noise is stripped and that the output file contains the real
// content. A successful round-trip is the integration-shape check
// the other tests don't cover.
func TestRun_HappyPath(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "schema.sql")
	canned := strings.Join([]string{
		"-- Dumped from database version 16.14",
		"CREATE TABLE public.crons (",
		"    id uuid NOT NULL,",
		");",
		"-- PostgreSQL database dump complete",
	}, "\n")
	f := &fakeRunner{
		env:  "postgres://...",
		dump: []byte(canned),
	}

	if err := run(out, f); err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	for _, banned := range []string{"Dumped from database version", "PostgreSQL database dump complete"} {
		if strings.Contains(string(got), banned) {
			t.Errorf("output retained noise %q:\n%s", banned, got)
		}
	}
	if !strings.Contains(string(got), "CREATE TABLE public.crons") {
		t.Errorf("output lost schema content:\n%s", got)
	}
	if f.closeCount != 1 {
		t.Errorf("pool not closed; closeCount=%d, want 1", f.closeCount)
	}
}

// TestRun_WriteError is the regression for os.WriteFile failures
// (read-only mount, permission denied). Without this, a CI runner
// with a broken schema.sql permissions would surface as a generic
// "write" wrap; with it, the path is named in the error.
func TestRun_WriteError(t *testing.T) {
	// Use a path under a directory that does not exist — os.WriteFile
	// fails with "no such file or directory" and the path is preserved
	// in the wrap so the operator sees which file failed.
	out := filepath.Join(t.TempDir(), "does", "not", "exist", "schema.sql")
	f := &fakeRunner{
		env:  "postgres://...",
		dump: []byte("CREATE TABLE public.x (id int);\n"),
	}

	err := run(out, f)
	if err == nil {
		t.Fatal("run succeeded writing to a non-existent directory; want error")
	}
	if !strings.Contains(err.Error(), "write "+out+":") {
		t.Errorf("error = %q, want wrap of write with path %q", err, out)
	}
}
