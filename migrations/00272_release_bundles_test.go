//go:build !no_pg

// Migration-apply test for 00272 (issue #911 / ADR-110 PR-3a:
// release_bundles table storage).
//
// Pins the load-bearing contract that PR-3 (release bundle content +
// install) and PR-4 (gregale doctor) consume:
//
//   1. The migration set applies cleanly through 00272.
//   2. release_bundles table exists with the canonical columns:
//        id uuid PK DEFAULT gen_random_uuid()
//        git_sha text NOT NULL CHECK ~ '^[a-f0-9]{40}$'
//        manifest_hash text NOT NULL CHECK ~ '^sha256:[a-f0-9]{64}$'
//        daemon_hashes jsonb NOT NULL DEFAULT '{}'::jsonb
//        created_at timestamptz NOT NULL DEFAULT now()
//        applied_at timestamptz NULL
//   3. daemon_hashes defaults to '{}'::jsonb so PR-3 can stamp the
//      per-daemon hashes incrementally — the doctor's "match running
//      binary hashes" check relies on the column being a JSONB map
//      with daemon-name keys + sha256:<64hex> values.
//   4. CHECK constraints reject malformed git_sha / manifest_hash at
//      INSERT — load-bearing so PR-3's bundle creation can't poison
//      the table with mis-typed values that would defeat the doctor's
//      hash comparisons later.
//   5. Both indexes exist (release_bundles_git_sha_idx and the partial
//      release_bundles_applied_at_idx WHERE applied_at IS NOT NULL).
//      The partial form keeps the index small when most bundles are
//      unapplied (operator dashboard query).
//   6. INSERT a row that provides only git_sha + manifest_hash and
//      confirms daemon_hashes defaults, created_at defaults, applied_at
//      is nullable — the canonical PR-3a "ship a bundle" smoke test.
//
// Build tag mirrors 00072_compute_nodes_region_zone_test.go:1 —
// FAAS_SKIP_PG_TESTS=1 locally skips.

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00272_ReleaseBundles(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (1) Table exists.
	var tableName string
	if err := pool.QueryRow(ctx, `
		select table_name
		  from information_schema.tables
		 where table_schema = current_schema()
		   and table_name   = 'release_bundles'
	`).Scan(&tableName); err != nil {
		t.Fatalf("release_bundles table missing after 00272 apply: %v", err)
	}

	// (2) Each column has the right type + nullability.
	type colSpec struct {
		name     string
		dataType string
		nullable string // YES | NO
	}
	want := []colSpec{
		{"id", "uuid", "NO"},
		{"git_sha", "text", "NO"},
		{"manifest_hash", "text", "NO"},
		{"daemon_hashes", "jsonb", "NO"},
		{"created_at", "timestamp with time zone", "NO"},
		{"applied_at", "timestamp with time zone", "YES"},
	}
	for _, c := range want {
		var dataType, nullable string
		if err := pool.QueryRow(ctx, `
			select data_type, is_nullable
			  from information_schema.columns
			 where table_schema = current_schema()
			   and table_name   = 'release_bundles'
			   and column_name  = $1
		`, c.name).Scan(&dataType, &nullable); err != nil {
			t.Errorf("release_bundles.%s not present: %v", c.name, err)
			continue
		}
		if dataType != c.dataType {
			t.Errorf("release_bundles.%s data_type = %q, want %q", c.name, dataType, c.dataType)
		}
		if nullable != c.nullable {
			t.Errorf("release_bundles.%s is_nullable = %q, want %q", c.name, nullable, c.nullable)
		}
	}

	// (3) CHECK constraints reject malformed git_sha / manifest_hash.
	//     Each rejected INSERT must surface SQLSTATE 23514 (check_violation);
	//     a typo that slips past the regex would defeat the doctor's
	//     hash comparison downstream.
	for _, tc := range []struct {
		name     string
		sha      string
		manifest string
	}{
		{"short git_sha", "abc123", "sha256:" + hexOfLen(64)},
		{"non-hex git_sha", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "sha256:" + hexOfLen(64)},
		{"bad manifest_hash shape", hexOfLen(40), "not-sha256:" + hexOfLen(64)},
		{"short manifest_hash", hexOfLen(40), "sha256:" + hexOfLen(20)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `
				insert into release_bundles (git_sha, manifest_hash)
				values ($1, $2)
			`, tc.sha, tc.manifest)
			if err == nil {
				t.Errorf("insert release_bundles(%q, %q) succeeded; want CHECK violation", tc.sha, tc.manifest)
				return
			}
			// Don't assert SQLSTATE 23514 precisely (pgx wraps errors);
			// substring probe is enough to catch "no error at all".
			if !strings.Contains(err.Error(), "violates check constraint") &&
				!strings.Contains(err.Error(), "check constraint") {
				t.Errorf("insert error = %v, want check constraint violation", err)
			}
		})
	}

	// (4) Both indexes exist. The partial one carries a WHERE
	//     predicate; we substring-probe to confirm the form.
	idxCases := []struct {
		name   string
		expect string // substring must appear in pg_indexes.indexdef
	}{
		{"release_bundles_git_sha_idx", ""},
		{"release_bundles_applied_at_idx", "WHERE (applied_at IS NOT NULL)"},
	}
	for _, c := range idxCases {
		var idxDef string
		if err := pool.QueryRow(ctx, `
			select indexdef
			  from pg_indexes
			 where schemaname = current_schema()
			   and tablename  = 'release_bundles'
			   and indexname  = $1
		`, c.name).Scan(&idxDef); err != nil {
			t.Errorf("%s missing: %v", c.name, err)
			continue
		}
		if c.expect != "" && !strings.Contains(idxDef, c.expect) {
			t.Errorf("%s indexdef = %q, want substring %q", c.name, idxDef, c.expect)
		}
	}

	// (5) Canonical PR-3a "ship a bundle" smoke test: insert only
	//     git_sha + manifest_hash, confirm the defaults.
	const gitSHA = "0123456789abcdef0123456789abcdef01234567" // 40 hex
	manifest := "sha256:" + hexOfLen(64)                      // var because hexOfLen is a function call
	var (
		gotDaemonHashes string
		gotCreatedAt    time.Time
		gotAppliedAt    *time.Time // nullable
	)
	before := time.Now().UTC().Add(-2 * time.Second)
	if err := pool.QueryRow(ctx, `
		insert into release_bundles (git_sha, manifest_hash)
		values ($1, $2)
		returning daemon_hashes, created_at, applied_at
	`, gitSHA, manifest).Scan(&gotDaemonHashes, &gotCreatedAt, &gotAppliedAt); err != nil {
		t.Fatalf("insert release_bundles defaults probe: %v", err)
	}
	if gotDaemonHashes != "{}" {
		t.Errorf("daemon_hashes default = %q, want '{}'", gotDaemonHashes)
	}
	if gotCreatedAt.Before(before) {
		t.Errorf("created_at = %v, want >= now() (default now())", gotCreatedAt)
	}
	if gotAppliedAt != nil {
		t.Errorf("applied_at = %v, want NULL (nullable default)", *gotAppliedAt)
	}
}

// hexOfLen returns an n-char lowercase-hex string. The release_bundles
// CHECKs are pure shape regexes (`^[a-f0-9]{40}$` for git_sha,
// `^sha256:[a-f0-9]{64}$` for manifest_hash), so the digits themselves
// are not load-bearing — the LENGTH is. The predecessor helpers took an
// int and ignored it, which quietly made the "short manifest_hash"
// negative case feed a perfectly valid 64-char digest and assert
// nothing; keep the length a real parameter.
func hexOfLen(n int) string {
	return strings.Repeat("0", n)
}
