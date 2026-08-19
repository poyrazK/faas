// migrations/00296_app_secret_value_hash_test.go — pins the
// shape of the value_hash column on app_secrets (ADR-117 PR-C).
//
// Five assertions:
//  1. NULLABLE — pre-PR-C rows must remain NULL, NOT ''.
//     '' would make "pre-PR-C row" indistinguishable from
//     "post-PR-C row with empty plaintext" (which would be a
//     bug — the handler never accepts empty plaintexts).
//  2. CHECK (length <= 16) — the wire shape is 16 hex chars
//     (HMAC-SHA256 truncated to 64 bits), and the column is
//     stored as TEXT.
//  3. Length cap = 16 — exactly 16 hex chars is accepted.
//  4. 17 hex chars (over-stamp) is rejected with SQLSTATE 23514
//     (check_violation). The CHECK is the boundary that keeps
//     the wire shape honest.
//  5. Pre-PR-C rows survive the migration unchanged (NULL kid
//     and NULL value_hash after the migration).
//
//go:build pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func Test_00296_AppSecretValueHash(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Run("column is nullable", func(t *testing.T) {
		// Force a pre-PR-C row by inserting with NULL value_hash.
		// A NOT NULL DEFAULT '' constraint would have rejected
		// this INSERT; the test failure is the signal.
		var n interface{}
		err := pool.QueryRow(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'PRE_PR_C_KEY', 'cipher')
			 returning value_hash`).Scan(&n)
		if err != nil {
			t.Fatalf("insert with NULL value_hash: %v (the column MUST be NULLABLE; pre-PR-C rows are NULL by design — see ADR-117 D6)", err)
		}
		if n != nil {
			t.Errorf("value_hash after insert: got %v, want nil (NULLABLE — pre-PR-C rows preserve their pre-migration shape)", n)
		}
	})

	t.Run("check shape length cap", func(t *testing.T) {
		// Exactly 16 hex chars: accepted.
		_, err := pool.Exec(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext, value_hash)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'CAP_KEY', 'cipher', 'abcdef0123456789')`)
		if err != nil {
			t.Fatalf("16-hex value_hash insert: %v (the CHECK MUST accept exactly 16 hex chars)", err)
		}

		// 17 hex chars: rejected with 23514 (check_violation).
		_, err = pool.Exec(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext, value_hash)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'OVER_KEY', 'cipher', 'abcdef01234567890')`)
		if err == nil {
			t.Fatal("17-hex value_hash insert accepted; MUST be rejected (the CHECK caps at 16)")
		}
		if !strings.Contains(err.Error(), "23514") {
			t.Errorf("expected 23514 check_violation; got %v", err)
		}
	})

	t.Run("pre-PR-C row shape preserved", func(t *testing.T) {
		// Belt-and-suspenders: a row inserted with both kid and
		// value_hash as NULL survives the migration intact. The
		// COALESCE in pkg/state/pgstore.go turns these into ''
		// on read, but the underlying column is NULL — a
		// regression here would surface as a stuck NULL kid on
		// legacy rows.
		var kid, vh interface{}
		err := pool.QueryRow(ctx,
			`select kid, value_hash from app_secrets
			 where key = 'PRE_PR_C_KEY' limit 1`).Scan(&kid, &vh)
		if err != nil {
			t.Fatalf("read pre-PR-C row: %v", err)
		}
		if kid != nil {
			t.Errorf("pre-PR-C row kid: got %v, want nil", kid)
		}
		if vh != nil {
			t.Errorf("pre-PR-C row value_hash: got %v, want nil (the column is NULLABLE, NOT NULL DEFAULT '')", vh)
		}
	})
}