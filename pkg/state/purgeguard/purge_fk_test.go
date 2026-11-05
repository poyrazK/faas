// Package purgeguard_test checks the permanent app and account purges in
// pkg/state against schema.sql: every foreign key that can refuse one of their
// deletes must be cleared first. Each gap failed purges in production for
// weeks before anyone saw it (2026-11-03: exclusive_work_operations held
// every purge of an app that had run exclusive work).
//
// The check is static. It reads the purge functions' source in order, maps
// each DELETE (inline SQL or a sqlc query) to the table it removes rows from,
// spreads that through ON DELETE CASCADE keys, and then requires every
// RESTRICT or NO ACTION key into a removed table to have its referencing rows
// removed earlier (or, for a deferred key, by commit). Partial deletes count
// as full ones: the purge refuses up front while a row it must keep exists.
package purgeguard_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type foreignKey struct {
	name, table, ref string
	action           string // cascade, set_null, restrict, no_action
	deferred         bool
}

var fkPattern = regexp.MustCompile(`(?s)ALTER TABLE (?:ONLY )?public\.(\w+)\s+ADD CONSTRAINT (\w+) FOREIGN KEY \([^)]*\) REFERENCES public\.(\w+)\([^)]*\)([^;]*);`)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func schemaForeignKeys(t *testing.T) []foreignKey {
	t.Helper()
	var out []foreignKey
	for _, m := range fkPattern.FindAllStringSubmatch(readFile(t, "../../../schema.sql"), -1) {
		rest := m[4]
		fk := foreignKey{name: m[2], table: m[1], ref: m[3], action: "no_action",
			deferred: strings.Contains(rest, "INITIALLY DEFERRED")}
		switch {
		case strings.Contains(rest, "ON DELETE CASCADE"):
			fk.action = "cascade"
		case strings.Contains(rest, "ON DELETE SET NULL"), strings.Contains(rest, "ON DELETE SET DEFAULT"):
			fk.action = "set_null"
		case strings.Contains(rest, "ON DELETE RESTRICT"):
			fk.action = "restrict"
		}
		out = append(out, fk)
	}
	if len(out) < 100 {
		t.Fatalf("parsed only %d foreign keys from schema.sql", len(out))
	}
	return out
}

var (
	queryNamePattern = regexp.MustCompile(`(?m)^-- name: (\w+)`)
	deletePattern    = regexp.MustCompile(`(?i)\bdelete\s+from\s+(?:public\.)?(\w+)`)
)

// sqlcDeletes maps every sqlc query under pkg/state whose statement is a
// DELETE to the table it deletes from.
func sqlcDeletes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") || strings.HasSuffix(path, "schema.sql") {
			return err
		}
		src := readFile(t, path)
		idx := queryNamePattern.FindAllStringSubmatchIndex(src, -1)
		for i, m := range idx {
			end := len(src)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			body := src[m[1]:end]
			if nl := strings.IndexByte(body, '\n'); nl >= 0 {
				body = body[nl+1:] // past the ":exec" annotation
			}
			if del := deletePattern.FindStringSubmatch(body); del != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(stripComments(body))), "delete") {
				out[src[m[2]:m[3]]] = del[1]
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func stripComments(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// goBlock returns the source from the line starting with header to its
// closing brace at column zero.
func goBlock(t *testing.T, src, header string) string {
	t.Helper()
	start := strings.Index(src, header)
	if start < 0 {
		t.Fatalf("source has no %q", header)
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("no end for %q", header)
	}
	return src[start : start+end]
}

var (
	// tokenPattern finds, in order, inline DELETE statements and calls.
	// An "update <table> set <col> = null" clears that table's references
	// without deleting its rows; it is recorded as "~<table>".
	tokenPattern = regexp.MustCompile("(?i)delete\\s+from\\s+(\\w+)|\\.(\\w+)\\(|\\b(purgeOperationOwnerTx|appPurgeSteps|accountGitJobPurgeSteps)\\b|update\\s+(\\w+)\\s+set\\s+\\w+\\s*=\\s*null\\b")
)

// removals lists the tables a purge deletes from, in the order it deletes,
// with helpers expanded.
func removals(t *testing.T, body string, helpers map[string]string, queries map[string]string) []string {
	t.Helper()
	var out []string
	// Comments quote statements ("the `delete from accounts` at the bottom").
	var code strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code.WriteString(line)
			code.WriteByte('\n')
		}
	}
	for _, m := range tokenPattern.FindAllStringSubmatch(code.String(), -1) {
		switch {
		case m[1] != "":
			out = append(out, m[1])
		case m[2] != "":
			if table, ok := queries[m[2]]; ok {
				out = append(out, table)
			}
		case m[3] != "":
			out = append(out, removals(t, helpers[m[3]], nil, queries)...)
		case m[4] != "":
			out = append(out, "~"+m[4])
		}
	}
	return out
}

type exemption struct{ table, ref, why string }

// check returns the keys that would refuse a delete in sequence. With
// explicitOnly, only keys into tables the purge deletes from itself count:
// the account purge removes the account's personal rows from tables it
// shares with organizations, so a cascade from the account does not reach
// every row of a table the cascade passes through.
func check(fks []foreignKey, sequence []string, exempt []exemption, explicitOnly bool) []string {
	const never = 1 << 30
	at := map[string]int{}
	explicit := map[string]bool{}
	cleared := map[string]int{}
	for i, table := range sequence {
		if name, ok := strings.CutPrefix(table, "~"); ok {
			if _, seen := cleared[name]; !seen {
				cleared[name] = i
			}
			continue
		}
		explicit[table] = true
		if _, seen := at[table]; !seen {
			at[table] = i
		}
	}
	removedAt := func(table string) int {
		if i, ok := at[table]; ok {
			return i
		}
		return never
	}
	for changed := true; changed; {
		changed = false
		for _, fk := range fks {
			if fk.action != "cascade" || fk.table == fk.ref {
				continue
			}
			if r := removedAt(fk.ref); r < removedAt(fk.table) {
				at[fk.table] = r
				changed = true
			}
		}
	}
	skip := map[[2]string]bool{}
	for _, e := range exempt {
		skip[[2]string{e.table, e.ref}] = true
	}
	var bad []string
	for _, fk := range fks {
		if fk.action == "cascade" || fk.action == "set_null" || fk.table == fk.ref || skip[[2]string{fk.table, fk.ref}] || skip[[2]string{fk.table, ""}] {
			continue
		}
		if explicitOnly && !explicit[fk.ref] {
			continue
		}
		r := removedAt(fk.ref)
		if r == never {
			continue
		}
		t := removedAt(fk.table)
		if c, ok := cleared[fk.table]; ok && c < t {
			t = c
		}
		switch {
		case fk.deferred && t != never:
		case t < r:
		case t == r:
			// Both rows go in the same statement (one cascades from the
			// other's delete), so the key no longer sees a referencing row.
		default:
			bad = append(bad, fk.table+" -> "+fk.ref+" ("+fk.name+")")
		}
	}
	sort.Strings(bad)
	return bad
}

func TestAppPurgeClearsEveryRestrictingKey(t *testing.T) {
	fks := schemaForeignKeys(t)
	queries := sqlcDeletes(t)
	pgstore := readFile(t, "../pgstore.go")
	helpers := map[string]string{
		"purgeOperationOwnerTx": goBlock(t, readFile(t, "../operations_invocation_row.go"), "func purgeOperationOwnerTx("),
		"appPurgeSteps":         goBlock(t, pgstore, "var appPurgeSteps = "),
	}
	sequence := removals(t, goBlock(t, pgstore, "func (s *PgStore) DeleteAppPermanently("), helpers, queries)
	if os.Getenv("PURGE_DEBUG") != "" {
		t.Logf("sequence: %v", sequence)
	}
	if len(sequence) < 30 || sequence[len(sequence)-1] != "apps" {
		t.Fatalf("parsed app purge sequence looks wrong (%d steps, last %q): %v", len(sequence), sequence[len(sequence)-1], sequence)
	}
	exempt := []exemption{
		// DeleteAppPermanently refuses (environment_clone) while an
		// environment clone fences or targets one of the app's buckets.
		{"object_bucket_write_fences", "object_buckets", "purge refuses: environment_clone"},
		{"project_environment_clone_checkpoint_object_targets", "object_buckets", "purge refuses: environment_clone"},
		{"project_environment_clone_checkpoint_unused_object_reservations", "object_buckets", "purge refuses: environment_clone"},
		{"project_environment_clone_postgres_bindings", "managed_postgres_bindings", "purge refuses: environment_clone"},
		// ... or (managed_postgres_binding) while a cutover of the app is
		// still named by the binding service's rows.
		{"managed_postgres_databases", "managed_postgres_cutovers", "purge refuses: managed_postgres_binding"},
		{"managed_postgres_checkpoint_maintenance", "managed_postgres_cutovers", "purge refuses: managed_postgres_binding"},
		{"managed_postgres_cutover_source_fences", "managed_postgres_cutovers", "purge refuses: managed_postgres_binding"},
		// An active migration lease is held by a running instance. A deleted
		// app has none: soft delete stops its pipeline and schedd destroys its
		// instances long before the grace period ends. A stale one fails the
		// purge as foreign_key, which alerts.
		{"migration_admissions", "migration_leases", "no running instance after grace"},
		{"migration_sources", "migration_leases", "no running instance after grace"},
	}
	for _, bad := range check(fks, sequence, exempt, false) {
		t.Errorf("app purge: a row of %s would refuse it; delete it in appPurgeSteps before the row it references, or make the purge wait for it", bad)
	}
}

func TestAccountPurgeClearsEveryRestrictingKey(t *testing.T) {
	fks := schemaForeignKeys(t)
	queries := sqlcDeletes(t)
	pgstore := readFile(t, "../pgstore.go")
	helpers := map[string]string{
		"purgeOperationOwnerTx":   goBlock(t, readFile(t, "../operations_invocation_row.go"), "func purgeOperationOwnerTx("),
		"accountGitJobPurgeSteps": goBlock(t, readFile(t, "../account_purge.go"), "var accountGitJobPurgeSteps = "),
	}
	body := goBlock(t, pgstore, "func (s *PgStore) DeleteAccount(")
	// Everything after the sentinel delete runs once the account is gone.
	if i := strings.Index(body, "`delete from accounts where id = $1"); i > 0 {
		body = body[:i] + "`delete from accounts`"
	}
	sequence := removals(t, body, helpers, queries)
	if len(sequence) < 20 || sequence[len(sequence)-1] != "accounts" {
		t.Fatalf("parsed account purge sequence looks wrong (%d steps): %v", len(sequence), sequence)
	}
	for _, bad := range check(fks, sequence, accountPurgeExemptions, true) {
		t.Errorf("account purge: a row of %s would refuse it; delete it before the row it references, or make the purge wait for it", bad)
	}
}

// accountPurgeExemptions are keys the account purge does not clear because
// it refuses, up front, to run while such a row exists.
// They are the tables state.accountPurgeWaitsQuery counts; an empty ref
// exempts every key of the table.
var accountPurgeExemptions = []exemption{
	// hosting_site: the grace sweep deletes the sites, the hosting
	// reconciler purges them with their domains, previews and trusts.
	{"hosting_sites", "", "purge refuses: hosting_site"},
	{"hosting_domains", "", "purge refuses: hosting_site"},
	{"hosting_previews", "", "purge refuses: hosting_site"},
	{"hosting_oidc_trusts", "", "purge refuses: hosting_site"},
	{"hosting_automation_credentials", "", "purge refuses: hosting_site"},
	// environment_clone: the clone workflow releases its state.
	{"project_environment_clone_checkpoint_postgres_credentials", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_archives", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_contents", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_copy_readers", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_copy_targets", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_database_plans", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_database_sql_pins", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_imports", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_inventories", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_membership_plans", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_role_plans", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_snapshot_restores", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_target_sql_pins", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_verification_read_budgets", "", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_verifications", "", "purge refuses: environment_clone"},
	{"project_environment_clone_checkpoint_postgres_isolation", "managed_postgres_databases", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_snapshots", "managed_postgres_databases", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_write_fences", "managed_postgres_databases", "purge refuses: environment_clone"},
	{"project_environment_clone_postgres_bindings", "managed_postgres_bindings", "purge refuses: environment_clone"},
	{"object_bucket_write_fences", "object_buckets", "purge refuses: environment_clone"},
	{"project_environment_clone_checkpoint_object_targets", "object_buckets", "purge refuses: environment_clone"},
	{"project_environment_clone_checkpoint_unused_object_reservations", "object_buckets", "purge refuses: environment_clone"},
	// managed_postgres_fence: the managed PostgreSQL service owns them.
	{"managed_postgres_checkpoint_maintenance", "managed_postgres_databases", "purge refuses: managed_postgres_fence"},
	{"managed_postgres_cutover_source_fences", "managed_postgres_databases", "purge refuses: managed_postgres_fence"},
	// Append-only accounting evidence: its trigger refuses a delete while the
	// account row exists, so the purge cannot remove it first. The key is
	// deferred, and the rows cascade from the account's resource namespace
	// after the account row is gone, before commit
	// (pkg/managedpostgres TestUsageImportEvidenceAllowsFinalAccountErasure,
	// TestAccountingReconciliationAuditReplayAndAccountErasure).
	{"managed_postgres_usage_imports", "managed_postgres_databases", "deferred; cascades after the account row"},
	{"managed_postgres_accounting_reconciliations", "managed_postgres_databases", "deferred; cascades after the account row"},
}
