//go:build !no_pg

// Backfill-semantics test for migration 00046 (IAM-1 / ADR-034 rev2).
//
// Asserts:
//
//   1. Every legacy row's new scopes match the expected expansion
//      (admin→admin, read→{apps:read,usage:read,secrets:read},
//      write→{deploy:write,secrets:write}, union when both).
//
//   2. The CHECK constraint added by 00046 rejects an INSERT with an
//      out-of-vocabulary scope.
//
//   3. Three `key.scopes_changed` audit rows landed with the right
//      from/to payload (actor='migration'). The {admin}-only seed is
//      untouched by the snapshot CTE predicate
//      `scopes && ARRAY['read','write']`, so it produces no audit
//      row — exactly 3 is the expected count for a 4-seed setup.
//
// Build tag mirrors apply_walk_test.go:4 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00046_APIKeyScopesV2_BackfillAndCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full migration set including 00046. The backfill
	// has already run as part of the apply; we'll insert four legacy
	// rows directly so the test can exercise the BEFORE state without
	// fighting the order of operations (the apply-set runs once per
	// process — leaving nothing to re-apply here).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (2) Seed the four legacy rows.
	const acctID = "00000000-0000-0000-0000-000000000046"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'iam1@example.com', 'hobby', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	// api_keys.org_id is NOT NULL with an FK to orgs (00099 added the
	// column + api_keys_org_id_fkey; 00134 flipped it NOT NULL), so a
	// key needs a real org parent. The org is not what 00046 pins —
	// it is just the FK parent that makes the scope-expansion seeds
	// insertable.
	orgID := seedOrg(t, ctx, pool)

	type seed struct {
		hashHex string
		scopes  []string
	}
	seeds := []seed{
		{"aaa1", []string{"admin"}},
		{"aaa2", []string{"read"}},
		{"aaa3", []string{"write"}},
		{"aaa4", []string{"read", "write", "admin"}},
	}
	// (3) Now disable the constraint (added by 00046) so the
	// pre-migration `{read}` and `{write}` rows can be seeded. The
	// backfill CTE is exactly what we're testing here — we don't
	// want the constraint to filter them out at INSERT time. We do
	// the round-trip the way the deployment pipeline would: drop the
	// constraint, apply the backfill CTE, re-add the constraint.
	if _, err := pool.Exec(ctx,
		`ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk`); err != nil {
		t.Fatalf("drop chk for setup: %v", err)
	}

	for _, s := range seeds {
		// Re-encode the hash to bytes[] so the column NOT NULL check passes.
		if _, err := pool.Exec(ctx, `
			insert into api_keys (id, account_id, key_sha256, label, scopes, org_id)
			values ($1, $2, decode($3, 'hex'), $4, $5, $6)
			on conflict (id) do update set scopes = excluded.scopes
		`, "00000000-0000-0000-0000-00000000"+s.hashHex, acctID, s.hashHex+"00000000000000000000000000000000000000000000000000000000", "legacy-"+s.hashHex, sortedStrings(s.scopes), orgID); err != nil {
			t.Fatalf("seed %s: %v", s.hashHex, err)
		}
	}
	// Apply the backfill CTE literally (mirrors migrations/00046.sql).
	if _, err := pool.Exec(ctx, `
		WITH snapshot AS (
		    SELECT id, account_id, scopes AS old_scopes
		      FROM api_keys
		     WHERE account_id = $1
		       AND scopes && ARRAY['read','write']::text[]
		     FOR UPDATE
		),
		backfilled AS (
		    UPDATE api_keys k
		       SET scopes = (
		           SELECT array_agg(DISTINCT v)
		             FROM unnest(
		                 ARRAY_CAT(
		                     ARRAY_CAT(
		                         CASE WHEN 'admin' = ANY(s.old_scopes) THEN ARRAY['admin']::text[] ELSE ARRAY[]::text[] END,
		                         CASE WHEN 'write' = ANY(s.old_scopes) THEN ARRAY['deploy:write','secrets:write']::text[] ELSE ARRAY[]::text[] END
		                     ),
		                     CASE WHEN 'read' = ANY(s.old_scopes) THEN ARRAY['apps:read','usage:read','secrets:read']::text[] ELSE ARRAY[]::text[] END
		                 )
		             ) AS v
		           )
		      FROM snapshot s
		     WHERE k.id = s.id
		     RETURNING k.id, k.account_id, k.scopes AS new_scopes, s.old_scopes
		)
		INSERT INTO events (actor, kind, subject, data)
		SELECT 'migration', 'key.scopes_changed', account_id,
		       jsonb_build_object('key_id', id, 'from', old_scopes, 'to', new_scopes)
		  FROM backfilled
	`, acctID); err != nil {
		t.Fatalf("backfill cte: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE api_keys
		    ADD CONSTRAINT api_keys_scopes_vocab_chk
		    CHECK (scopes <@ ARRAY['admin','deploy:write','secrets:read','secrets:write','usage:read','apps:read']::text[]
		           AND cardinality(scopes) > 0)
	`); err != nil {
		t.Fatalf("re-add chk: %v", err)
	}

	// (4) Verify each row's scopes match the expected expansion.
	wantByHash := map[string][]string{
		"aaa1": {"admin"},
		"aaa2": {"apps:read", "usage:read", "secrets:read"},
		"aaa3": {"deploy:write", "secrets:write"},
		"aaa4": {"admin", "apps:read", "usage:read", "secrets:read", "deploy:write", "secrets:write"},
	}
	rows, err := pool.Query(ctx, `select key_sha256, scopes from api_keys where account_id = $1`, acctID)
	if err != nil {
		t.Fatalf("query keys: %v", err)
	}
	gotByHash := map[string][]string{}
	for rows.Next() {
		var raw []byte
		var sc []string
		if err := rows.Scan(&raw, &sc); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		gotByHash[hexPrefix(raw, 4)] = sc
	}
	rows.Close()
	for hash, want := range wantByHash {
		got := sortedStrings(gotByHash[hash])
		if !equalSets(got, sortedStrings(want)) {
			t.Errorf("hash %s: got %v, want %v", hash, got, want)
		}
	}

	// (5) The CHECK constraint now rejects an INSERT with an unknown scope.
	// The org_id is a live FK parent here on purpose: with a dangling
	// org the INSERT would fail 23503 and the test would "pass" without
	// ever reaching api_keys_scopes_vocab_chk. Assert the SQLSTATE so
	// the CHECK is what actually rejects the row.
	_, err = pool.Exec(ctx, `
		insert into api_keys (id, account_id, key_sha256, label, scopes, org_id)
		values ('00000000-0000-0000-0000-000000000099', $1, decode('9999000000000000000000000000000000000000000000000000000000000000', 'hex'),
		        'bad', ARRAY['not-a-scope'], $2)
	`, acctID, orgID)
	if err == nil {
		t.Fatalf("expected chk violation for unknown scope")
	}
	var vocabErr *pgconn.PgError
	if !errors.As(err, &vocabErr) {
		t.Fatalf("unknown-scope insert: got non-Postgres error: %v", err)
	}
	if vocabErr.Code != "23514" {
		t.Errorf("unknown-scope insert: got SQLSTATE=%s (%s), want 23514 (api_keys_scopes_vocab_chk)",
			vocabErr.Code, vocabErr.ConstraintName)
	}

	// (6) Audit rows landed with the correct from/to payload. The query
	// orders by sorted-from, so wantAudit must be in the same order
	// for row-by-row comparison.
	wantAudit := []struct {
		from []string
		to   []string
	}{
		// sorted-from "admin,read,write" — the {read,write,admin} seed
		{[]string{"admin", "read", "write"}, []string{"admin", "apps:read", "deploy:write", "secrets:read", "secrets:write", "usage:read"}},
		// sorted-from "read" — the {read} seed
		{[]string{"read"}, []string{"apps:read", "secrets:read", "usage:read"}},
		// sorted-from "write" — the {write} seed
		{[]string{"write"}, []string{"deploy:write", "secrets:write"}},
	}
	rows, err = pool.Query(ctx, `
		select array( select jsonb_array_elements_text(data->'from') order by 1 ),
		       array( select jsonb_array_elements_text(data->'to') order by 1 )
		  from events
		 where actor = 'migration' and kind = 'key.scopes_changed' and subject = $1
		 order by array_to_string(array( select jsonb_array_elements_text(data->'from') order by 1 ), ',')
	`, acctID)
	if err != nil {
		t.Fatalf("query audit: %v", err)
	}
	got := []struct {
		from []string
		to   []string
	}{}
	for rows.Next() {
		var from, to []string
		if err := rows.Scan(&from, &to); err != nil {
			rows.Close()
			t.Fatalf("scan audit: %v", err)
		}
		got = append(got, struct {
			from []string
			to   []string
		}{from, to})
	}
	rows.Close()
	if len(got) != len(wantAudit) {
		t.Fatalf("audit row count = %d, want %d", len(got), len(wantAudit))
	}
	for i, want := range wantAudit {
		if !equalSets(got[i].from, want.from) {
			t.Errorf("audit[%d] from = %v, want %v", i, got[i].from, want.from)
		}
		if !equalSets(got[i].to, want.to) {
			t.Errorf("audit[%d] to = %v, want %v", i, got[i].to, want.to)
		}
	}
}

func sortedStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func equalSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hexPrefix returns the first n/2 bytes of raw as a hex string — the
// fixture key ids above use 4-nibble hashes so the assertions stay
// readable in failure output.
func hexPrefix(raw []byte, n int) string {
	const hexchars = "0123456789abcdef"
	if n > len(raw)*2 {
		n = len(raw) * 2
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b := raw[i/2]
		if i%2 == 0 {
			out[i] = hexchars[b>>4]
		} else {
			out[i] = hexchars[b&0x0f]
		}
	}
	return string(out)
}
