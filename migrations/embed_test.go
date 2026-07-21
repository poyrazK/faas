package migrations

// Static migration-contiguity check — fails fast on PR builds when the
// migration set on main doesn't form a clean 1..N sequence.
//
// Background: PR #93's deploy (commit 5fbc0e3) failed at the migrate step
// with "goose: error: found 1 missing migrations before current version 21:
// version 14". PR #83's earlier deploy had bumped the prod DB to v21 by
// walking 13 → 15 cleanly (PR #77 with v14 hadn't merged yet), so the v14
// gap went undetected at PR-time. This test catches the same failure mode
// for any future slot — including the v19 gap that ships on origin/main
// today (00018 → 00020).
//
// Migrations are append-only and contiguous; never skip a slot. Per
// migrations/README.md and spec §5.

import (
	"bufio"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// MigrationFile is one parsed entry in the embedded migration set. Exported
// so apply_walk_test.go (external test package) can reuse the parsing
// rules without forking them; if the filename convention ever changes,
// both packages see the change in lockstep.
type MigrationFile struct {
	Version int64
	Name    string // filename, e.g. "00014_cli_auth_codes.sql"
}

// nameRe matches the goose "NNNNN_name.sql" convention. The leading digits
// can be any length — the repo currently uses 5-digit prefixes uniformly,
// but \d+ leaves room for future growth past 99,999 migrations.
var nameRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

// filenameCommentRe matches an optional "-- filename: NNNNN_name.sql" line
// in the migration header. The check is additive: a file without this
// comment passes; a file with this comment must match its actual filename.
// No existing migration has this comment today (introduced as a convention
// alongside this PR for new migrations).
var filenameCommentRe = regexp.MustCompile(`^-- filename:\s*(\S+)\s*$`)

// LoadMigrations reads every embedded *.sql file, parses its filename, and
// returns the set sorted by version. Files that don't match the
// NNNNN_name.sql pattern are reported via t.Errorf and skipped — they
// would be silently dropped by goose anyway, but a parse failure here is
// the only signal at PR time that the convention has drifted.
//
// Exported so apply_walk_test.go can reuse it from a different test
// package without re-implementing the regex / sort. Keep the surface
// minimal: returns the parsed set, leaves per-file diagnostics (Up/Down
// directive, filename comment) to the embedded-side tests.
func LoadMigrations(t *testing.T) []MigrationFile {
	t.Helper()

	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	var out []MigrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue // README.md, embed.go, etc.
		}
		m := nameRe.FindStringSubmatch(e.Name())
		if m == nil {
			t.Errorf("migration filename %q does not match NNNNN_name.sql convention", e.Name())
			continue
		}
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			t.Errorf("migration %q: parsing prefix %q: %v", e.Name(), m[1], err)
			continue
		}
		out = append(out, MigrationFile{Version: v, Name: e.Name()})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// TestMigrationsContiguous asserts the embedded migration set is exactly
// {1, 2, …, N} with no gaps. A gap means goose's strict
// findMissingMigrations will refuse to apply future deploys whose binary
// embeds a slot the DB is past — the failure mode that bit PR #93's
// deploy (run 29841378918). The first missing slot is reported; full
// contiguity is required for the check to pass.
func TestMigrationsContiguous(t *testing.T) {
	files := LoadMigrations(t)
	if len(files) == 0 {
		t.Fatal("no embedded migrations; embed.go is empty?")
	}
	for i, f := range files {
		want := int64(i + 1)
		if f.Version != want {
			t.Errorf("migration slot %d is missing (got %s in position %d); migrations are append-only and contiguous, never skip a slot", want, f.Name, i+1)
			return // report first gap, not all
		}
	}
}

// TestMigrationsUniquePrefixes asserts no two files share the same NNNNN
// prefix. A collision here would panic goose at startup with "duplicate
// version N detected" — a failure mode the repo has hit twice already
// (PR #73 and PR #83 renumberings). Distinct from contiguity: two files
// both with prefix 14 would parse but produce the same version, which
// contiguity alone misses.
func TestMigrationsUniquePrefixes(t *testing.T) {
	files := LoadMigrations(t)
	seen := make(map[int64]string, len(files))
	for _, f := range files {
		if other, dup := seen[f.Version]; dup {
			t.Errorf("duplicate migration prefix %05d: %s and %s", f.Version, other, f.Name)
		}
		seen[f.Version] = f.Name
	}
}

// TestMigrationsGooseUpDirective asserts every migration file contains a
// "-- +goose Up" directive. Without it, goose silently skips the file —
// the table the migration was meant to create simply won't exist. Hard
// fail: every existing migration has Up today and every future migration
// must too.
func TestMigrationsGooseUpDirective(t *testing.T) {
	files := LoadMigrations(t)
	for _, f := range files {
		if !hasDirective(t, f.Name, "-- +goose Up") {
			t.Errorf("%s: missing '-- +goose Up' directive; goose will silently skip the file", f.Name)
		}
	}
}

// TestMigrationsGooseDownDirective is a soft warning. Three legacy
// migrations (00005_login_tokens.sql, 00006_deployment_logs.sql,
// 00007_github_binding.sql) lack "-- +goose Down"; hard-failing on
// absence would block merge of already-shipped migrations. Logs only,
// doesn't fail the test. Promote to t.Errorf once all migrations have
// Down (or backfill the missing directives in a separate PR).
func TestMigrationsGooseDownDirective(t *testing.T) {
	files := LoadMigrations(t)
	for _, f := range files {
		if !hasDirective(t, f.Name, "-- +goose Down") {
			t.Logf("%s: missing '-- +goose Down' directive (warn-only; backfill when convenient)", f.Name)
		}
	}
}

// TestMigrationsFilenameMatchesComment asserts that when a migration
// carries a "-- filename: NNNNN_name.sql" comment in its first 10 lines,
// that comment matches the actual filename. The rule is additive: a
// file without the comment passes; a file with a mismatching comment
// fails. Forward-looking — no existing migration has the comment, so
// the rule is dormant until a contributor opts in.
func TestMigrationsFilenameMatchesComment(t *testing.T) {
	files := LoadMigrations(t)
	for _, f := range files {
		got := readFirstFilenameComment(t, f.Name)
		if got == "" {
			continue // additive: no comment, no rule
		}
		if got != f.Name {
			t.Errorf("%s: header comment '-- filename: %s' does not match actual filename %q", f.Name, got, f.Name)
		}
	}
}

// hasDirective opens the named file and returns true if directive appears
// as a non-blank line within the first 20 lines. 20 is generous enough to
// catch a directive preceded by a copyright header but bounded so a SQL
// line containing "-- +goose Up" deep inside a migration doesn't count.
// Exact match is required. Note: goose's parser (v3.27.2
// internal/sqlparser/parser.go:319 extractAnnotation via strings.EqualFold)
// is case-insensitive at runtime, but the repo convention is exact-case
// and we enforce that here to keep the convention crisp. Migrating the
// test to case-insensitive match is a deliberate non-goal: case-correct
// directives read more uniformly in diffs and reviews.
//
// On read failure the helper reports via t.Errorf and returns false
// instead of t.Fatalf — the caller is in a per-file loop and a single
// unreadable file should not abort the sweep. (Reads can't realistically
// fail against an embed.FS at runtime; this is defence-in-depth.)
func hasDirective(t *testing.T, name, directive string) bool {
	t.Helper()
	data, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Errorf("read %s: %v", name, err)
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for i := 0; i < 20 && scanner.Scan(); i++ {
		if strings.TrimSpace(scanner.Text()) == directive {
			return true
		}
	}
	return false
}

// readFirstFilenameComment scans the first 10 lines of name for a
// "-- filename: …" comment and returns the captured filename, or ""
// if none is found. Anchored to line start with optional leading
// whitespace ignored.
//
// On read failure the helper reports via t.Errorf and returns "" so
// the caller's per-file loop continues. (See hasDirective for the
// reasoning on t.Errorf vs t.Fatalf here.)
func readFirstFilenameComment(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Errorf("read %s: %v", name, err)
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for i := 0; i < 10 && scanner.Scan(); i++ {
		m := filenameCommentRe.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if m != nil {
			return m[1]
		}
	}
	return ""
}
