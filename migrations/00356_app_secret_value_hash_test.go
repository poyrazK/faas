// migrations/00356_app_secret_value_hash_test.go — pins the
// shape of the value_hash column on app_secrets (ADR-117 PR-C).

//go:build pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func Test_00356_AppSecretValueHash(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Run("column is nullable", func(t *testing.T) {
		var n interface{}
		err := pool.QueryRow(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'PRE_PR_C_KEY', 'cipher')
			 returning value_hash`).Scan(&n)
		if err != nil {
			t.Fatalf("insert with NULL value_hash: %v (the column MUST be NULLABLE)", err)
		}
		if n != nil {
			t.Errorf("value_hash after insert: got %v, want nil", n)
		}
	})

	t.Run("check shape length cap", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext, value_hash)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'CAP_KEY', 'cipher', 'abcdef0123456789')`)
		if err != nil {
			t.Fatalf("16-hex value_hash insert: %v", err)
		}
		_, err = pool.Exec(ctx,
			`insert into app_secrets (account_id, app_id, scope, key, ciphertext, value_hash)
			 values ('00000000-0000-0000-0000-000000000001',
			         '00000000-0000-0000-0000-000000000002',
			         'default', 'OVER_KEY', 'cipher', 'abcdef01234567890')`)
		if err == nil {
			t.Fatal("17-hex value_hash insert accepted; MUST be rejected")
		}
		if !strings.Contains(err.Error(), "23514") {
			t.Errorf("expected 23514 check_violation; got %v", err)
		}
	})
}
