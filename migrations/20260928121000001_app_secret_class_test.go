//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func Test_20260928121000001_AppSecretClass(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	var class string
	if err := pool.QueryRow(ctx, `insert into app_secrets (account_id, app_id, scope, key, ciphertext)
		values ($1, $2, 'default', 'DEFAULT_CLASS', 'cipher')
		returning secret_class`, accountID, appID).Scan(&class); err != nil {
		t.Fatalf("insert legacy-compatible row: %v", err)
	}
	if class != "persistent" {
		t.Fatalf("default class = %q, want persistent", class)
	}
	_, err := pool.Exec(ctx, `insert into app_secrets (account_id, app_id, scope, key, ciphertext, secret_class)
		values ($1, $2, 'default', 'CLASS_KEY', 'cipher', 'ephemeral')`, accountID, appID)
	if err != nil {
		t.Fatalf("insert ephemeral row: %v", err)
	}
	_, err = pool.Exec(ctx, `insert into app_secrets (account_id, app_id, scope, key, ciphertext, secret_class)
		values ($1, $2, 'default', 'BAD_CLASS', 'cipher', 'unknown')`, accountID, appID)
	if err == nil || !strings.Contains(err.Error(), "23514") {
		t.Fatalf("invalid class insert error = %v, want check_violation", err)
	}
}
