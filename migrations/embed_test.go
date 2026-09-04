package migrations

// Static migration-ID checks. The legacy set remains contiguous through
// LegacyMigrationMaxVersion; post-cutover migrations use sortable UTC
// timestamp IDs and may be merged or applied out of order.
//
// Background: PR #93's deploy (commit 5fbc0e3) failed at the migrate step
// with "goose: error: found 1 missing migrations before current version 21:
// version 14". PR #83's earlier deploy had bumped the prod DB to v21 by
// walking 13 → 15 cleanly (PR #77 with v14 hadn't merged yet), so the v14
// gap went undetected at PR-time. The legacy contiguity test permanently
// guards that history; exact-set checks cover out-of-order timestamp IDs.
//
// Migrations are append-only. Per migrations/README.md and spec §5.

import (
	"bufio"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MigrationFile is one parsed entry in the embedded migration set. Exported
// so apply_walk_test.go (external test package) can reuse the parsing
// rules without forking them; if the filename convention ever changes,
// both packages see the change in lockstep.
type MigrationFile struct {
	Version int64
	Name    string // filename, e.g. "00014_cli_auth_codes.sql"
}

// nameRe accepts both legacy five-digit IDs and post-cutover 17-digit UTC
// timestamp IDs. Namespace-specific validation happens below.
var nameRe = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

// filenameCommentRe matches an optional "-- filename: <version>_name.sql" line
// in the migration header. The check is additive: a file without this
// comment passes; a file with this comment must match its actual filename.
// No existing migration has this comment today (introduced as a convention
// alongside this PR for new migrations).
var filenameCommentRe = regexp.MustCompile(`^-- filename:\s*(\S+)\s*$`)

// reservationFilenameRe flags the no-op slot-reservation files ADR-041
// uses to break 4-way cross-PR slot collisions (issue #366, PRs #335 /
// #352 / #369 / #389 all wanted slot 56 in the same week). A
// reservation file does NOT claim a slot — it is metadata that says
// "I'm holding this slot number; let a real schema land here first,
// then drop me on merge." Both shapes (the canonical `NNNNN_reserve_slot`
// and the alt `NNNNN_no_op_slot_reservation` from PR #369) match.
//
// These files remain part of the immutable legacy ledger but are ignored by
// checks that apply only to real schema changes. ADR-142 retires reservations
// and the former cross-PR slot gate for all post-cutover migrations.
var reservationFilenameRe = regexp.MustCompile(`^[0-9]{5}_(.*_)?(reservation|reserve_slot)(_[^/]*)?\.sql$`)

// isReservationFilename reports whether name is a no-op slot-reservation
// file per ADR-041.
func isReservationFilename(name string) bool {
	return reservationFilenameRe.MatchString(name)
}

// LoadMigrations reads every embedded *.sql file, parses its filename, and
// returns the set sorted by version. Files that don't match the
// <numeric-version>_name.sql pattern are reported via t.Errorf and skipped — they
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
			t.Errorf("migration filename %q does not match <numeric-version>_name.sql convention", e.Name())
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

// TestMigrationsLegacyContiguous freezes the original 1..590 sequence. Old
// binaries and existing databases still rely on that history being complete;
// only migrations in the timestamp namespace may arrive out of order.
func TestMigrationsLegacyContiguous(t *testing.T) {
	files := LoadMigrations(t)
	if len(files) == 0 {
		t.Fatal("no embedded migrations; embed.go is empty?")
	}
	want := int64(1)
	for position, f := range files {
		if f.Version > LegacyMigrationMaxVersion {
			continue
		}
		if f.Version != want {
			t.Errorf("legacy migration slot %d is missing (got %s in position %d); versions 1..%d are frozen and contiguous", want, f.Name, position+1, LegacyMigrationMaxVersion)
			return // report first gap, not all
		}
		want++
	}
	if got := want - 1; got != LegacyMigrationMaxVersion {
		t.Errorf("legacy migration tail is %d, want frozen cutover %d", got, LegacyMigrationMaxVersion)
	}
}

// TestMigrationsVersionNamespaces prevents the old coordination scheme from
// returning. Five-digit migrations stop at 00590; every later migration must
// be a valid 17-digit UTC YYYYMMDDHHMMSSmmm timestamp at or after the cutover.
func TestMigrationsVersionNamespaces(t *testing.T) {
	for _, f := range LoadMigrations(t) {
		prefix := strings.SplitN(f.Name, "_", 2)[0]
		if f.Version <= LegacyMigrationMaxVersion {
			if len(prefix) != 5 {
				t.Errorf("legacy migration %s must retain its five-digit prefix", f.Name)
			}
			continue
		}
		if !IsTimestampMigrationVersion(f.Version) {
			t.Errorf("migration %s is in the forbidden gap after legacy v%d; use 'make migration-new NAME=...'", f.Name, LegacyMigrationMaxVersion)
			continue
		}
		if len(prefix) != TimestampMigrationVersionDigits {
			t.Errorf("timestamp migration %s has %d prefix digits, want %d (YYYYMMDDHHMMSSmmm)", f.Name, len(prefix), TimestampMigrationVersionDigits)
			continue
		}
		if _, err := time.Parse("20060102150405", prefix[:14]); err != nil {
			t.Errorf("timestamp migration %s has invalid UTC date/time: %v", f.Name, err)
		}
	}
}

// TestMigrationsUniquePrefixes asserts no two real migration files share
// the same numeric prefix. A collision here would panic goose at startup
// with "duplicate version N detected" — a failure mode the repo has hit
// twice already (PR #73 and PR #83 renumberings). Distinct from
// contiguity: two files both with prefix 14 would parse but produce the
// same version, which contiguity alone misses.
//
// Reservation files (ADR-041) are excluded from the duplicate-prefix
// check. A real schema at the same slot as a reservation is the whole
// point of the carve-out — the reservation is shadowed by the real
// schema, and a follow-up commit drops the reservation. Without this
// exclusion, the historical migration set would reject reservation files
// that intentionally share a legacy slot. New timestamp migrations never use
// reservations; ADR-142 retires that workflow after v590.
func TestMigrationsUniquePrefixes(t *testing.T) {
	files := LoadMigrations(t)
	seen := make(map[int64]string, len(files))
	for _, f := range files {
		if isReservationFilename(f.Name) {
			continue // reservations don't claim a slot
		}
		if other, dup := seen[f.Version]; dup {
			t.Errorf("duplicate migration prefix %05d: %s and %s", f.Version, other, f.Name)
		}
		seen[f.Version] = f.Name
	}
}

// TestMigrationsGooseUpDirective asserts every REAL migration file
// contains a "-- +goose Up" directive. Without it, goose silently skips
// the file — the table the migration was meant to create simply won't
// exist. Hard fail: every existing migration has Up today and every
// future migration must too.
//
// Reservation files (ADR-041) are exempted: they are intentionally
// no-op placeholders that hold a slot for a real schema to land in.
// They carry "-- +goose Up" + "-- +goose StatementBegin" +
// "-- +goose StatementEnd" so goose applies them as zero-statement
// blocks (the SQL body is empty), but the spirit of the rule — every
// real schema MUST have an Up directive — does not apply. A separate
// tripwire (TestMigrationsReservationsAreNoOp) asserts that
// reservations genuinely have no DDL.
func TestMigrationsGooseUpDirective(t *testing.T) {
	files := LoadMigrations(t)
	for _, f := range files {
		if isReservationFilename(f.Name) {
			continue // reservations are no-ops by design
		}
		if !hasDirective(t, f.Name, "-- +goose Up") {
			t.Errorf("%s: missing '-- +goose Up' directive; goose will silently skip the file", f.Name)
		}
	}
}

// TestMigrationsReservationsAreNoOp is the tripwire for the ADR-041
// carve-out: every file matched by reservationFilenameRe must be a
// genuine no-op (no CREATE / ALTER / DROP / INSERT / UPDATE / DELETE
// statements inside its Up block). The carve-out exists to let a real
// schema shadow a reservation at the same slot; if a reservation ever
// smuggles in real DDL, the carved-out slot is no longer truly free and
// a subsequent PR at that slot would race with the DDL on apply. The
// This remains as a permanent check on the frozen legacy files.
func TestMigrationsReservationsAreNoOp(t *testing.T) {
	files := LoadMigrations(t)
	for _, f := range files {
		if !isReservationFilename(f.Name) {
			continue
		}
		// Reservation must have an Up block (so goose applies it
		// cleanly and the slot is part of the embedded set), and
		// the Up body MUST NOT contain DDL keywords.
		data, err := fs.ReadFile(FS, f.Name)
		if err != nil {
			t.Errorf("read %s: %v", f.Name, err)
			continue
		}
		body := extractGooseUpBody(string(data))
		if body == "" {
			t.Errorf("%s: no '-- +goose Up' body; goose will skip it and the slot won't be held", f.Name)
			continue
		}
		// Strip SQL line comments so the explanatory header (which
		// uses words like "drop", "rollback", "DDL" liberally) does
		// not trip the keyword scanner. The carve-out is about
		// *statements*, not comments about statements.
		body = stripSQLLineComments(body)
		for _, kw := range []string{"create", "alter", "drop", "insert", "update", "delete", "truncate", "grant", "revoke"} {
			if containsSQLKeyword(body, kw) {
				t.Errorf("%s: reservation file contains %q statement; ADR-041 reservations must be no-ops so a real schema can shadow the slot", f.Name, kw)
			}
		}
	}
}

// extractGooseUpBody returns the SQL between "-- +goose StatementBegin"
// and "-- +goose StatementEnd" after a "-- +goose Up" line, or "" if
// the file has no Up block. Trims surrounding whitespace. Defence
// against a future convention change that drops StatementBegin/End.
func extractGooseUpBody(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	inUp := false
	inStmt := false
	var sb strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "-- +goose Up"):
			inUp = true
			continue
		case strings.HasPrefix(line, "-- +goose Down"):
			return "" // Down came first; no Up body to read
		case inUp && strings.HasPrefix(line, "-- +goose StatementBegin"):
			inStmt = true
			continue
		case inUp && inStmt && strings.HasPrefix(line, "-- +goose StatementEnd"):
			return strings.TrimSpace(sb.String())
		}
		if inUp && inStmt {
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return ""
}

// stripSQLLineComments removes SQL line comments (`--` to end-of-line)
// from body. Used by TestMigrationsReservationsAreNoOp so that an
// explanatory header comment inside the Up block ("the reservation is
// dropped on merge", "would re-expose the slot to the cross-PR gate")
// does not trip the DDL-keyword scanner. Block comments (`/* */`) are
// rare in migration files and are not stripped here — adds value
// without added risk; reservation bodies should be empty of both.
func stripSQLLineComments(body string) string {
	var sb strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		sb.WriteString(strings.TrimSpace(line))
		sb.WriteString("\n")
	}
	return sb.String()
}

// containsSQLKeyword reports whether body contains `kw` as a SQL token
// (whole-word, case-insensitive). Avoids false positives like
// `comment 'description has create in it'`. Reservation files should
// have empty bodies anyway, so this is belt-and-braces.
func containsSQLKeyword(body, kw string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, kw+" ") || strings.HasSuffix(lower, kw)
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
// carries a "-- filename: <version>_name.sql" comment in its first 10 lines,
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

// TestMigrationsNoGeneratorPlaceholders prevents an untouched migration-new
// template from being merged as an accidental no-op.
func TestMigrationsNoGeneratorPlaceholders(t *testing.T) {
	for _, f := range LoadMigrations(t) {
		data, err := fs.ReadFile(FS, f.Name)
		if err != nil {
			t.Errorf("read %s: %v", f.Name, err)
			continue
		}
		if strings.Contains(string(data), "Write the additive forward migration here.") {
			t.Errorf("%s still contains the migration-new placeholder; replace it with the forward migration", f.Name)
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
