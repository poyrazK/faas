//go:build !no_pg

// ADR-224: an account release receiver is tenant-owned, not app-bound.
package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrations_AccountReleaseWebhookScope(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	owner := seedAccount(t, ctx, pool)
	other := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, owner)
	const target = "https://example.com/account-release"

	insert := func(app any, accountID, url, scope string, filter []string) (string, error) {
		if filter == nil {
			filter = []string{}
		}
		var id string
		err := pool.QueryRow(ctx, `
			insert into app_webhooks (app_id, account_id, target_url, secret_sealed, event_filter, scope)
			values ($1, $2, $3, $4, $5::text[], $6)
			returning id::text`, app, accountID, url, []byte("sealed"), filter, scope).Scan(&id)
		return id, err
	}
	wantCode := func(err error, code string) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != code {
			t.Fatalf("error = %v, want SQLSTATE %s", err, code)
		}
	}

	accountID, err := insert(nil, owner, target, "account", []string{"deployment.live", "rollout.completed"})
	if err != nil {
		t.Fatal(err)
	}
	appHookID, err := insert(appID, owner, target, "app", nil)
	if err != nil {
		t.Fatalf("app and account scopes should coexist at one URL: %v", err)
	}
	if _, err := insert(nil, owner, target, "account", []string{"rollout.aborted"}); err == nil {
		t.Fatal("duplicate account target accepted")
	} else {
		wantCode(err, "23505")
	}
	if _, err := insert(nil, other, target, "account", []string{"deployment.failed"}); err != nil {
		t.Fatalf("another account should be able to reuse target URL: %v", err)
	}
	for _, tc := range []struct {
		name    string
		app     any
		scope   string
		filter  []string
		account string
		code    string
	}{
		{"account_with_app", appID, "account", []string{"deployment.live"}, owner, "23514"},
		{"app_without_app", nil, "app", []string{"deployment.live"}, owner, "23514"},
		{"unknown_scope", nil, "organization", []string{"deployment.live"}, owner, "23514"},
		{"account_all_events", nil, "account", nil, owner, "23514"},
		{"account_other_event", nil, "account", []string{"app.parked"}, owner, "23514"},
		{"account_empty_event", nil, "account", []string{"deployment.live", ""}, owner, "23514"},
		{"missing_account", nil, "account", []string{"deployment.live"}, "00000000-0000-0000-0000-000000000000", "23503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := insert(tc.app, tc.account, "https://example.com/invalid-"+tc.name, tc.scope, tc.filter)
			wantCode(err, tc.code)
		})
	}

	store := state.NewPgStore(pool)
	accountHook, err := store.AppWebhookByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if accountHook.Scope != state.AppWebhookScopeAccount || accountHook.AppID != "" || accountHook.AccountID != owner {
		t.Errorf("account hook = %+v", accountHook)
	}
	appHook, err := store.AppWebhookByID(ctx, appHookID)
	if err != nil {
		t.Fatal(err)
	}
	if appHook.Scope != state.AppWebhookScopeApp || appHook.AppID != appID {
		t.Errorf("app hook = %+v", appHook)
	}
	forApp, err := store.ListAppWebhooksForApp(ctx, appID)
	if err != nil || len(forApp) != 1 || forApp[0].ID != appHookID {
		t.Errorf("app list = %+v, %v", forApp, err)
	}
	forAccount, err := store.ListAppWebhooksForAccount(ctx, owner)
	if err != nil || len(forAccount) != 2 {
		t.Errorf("account list = %+v, %v", forAccount, err)
	}

	input := state.AppWebhook{AppID: appID, AccountID: owner, TargetURL: "https://example.com/third", SecretSealed: []byte("sealed"), Enabled: true}
	_, err = store.CreateAppWebhookIfUnderQuota(ctx, input, api.Limits{WebhookPerApp: 10, WebhookPerAccount: 2})
	var quota *state.AppWebhookQuotaError
	if !errors.As(err, &quota) || quota.Scope != state.AppWebhookQuotaScopeAccount {
		t.Errorf("account hook omitted from quota: %v", err)
	}
	input.AccountID = other
	if _, err := store.CreateAppWebhookIfUnderQuota(ctx, input, api.Limits{WebhookPerApp: 10, WebhookPerAccount: 10}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("foreign-account app creation = %v, want ErrNotFound", err)
	}
	input.AccountID = owner
	input.Scope = state.AppWebhookScopeAccount
	if _, err := store.CreateAppWebhook(ctx, input); !errors.Is(err, state.ErrInvalidAppWebhookScope) {
		t.Errorf("app creation accepted account scope: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from apps where id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	var accountHookCount int
	if err := pool.QueryRow(ctx, `select count(*) from app_webhooks where id=$1`, accountID).Scan(&accountHookCount); err != nil {
		t.Fatal(err)
	}
	if accountHookCount != 1 {
		t.Errorf("deleting an app removed its account subscription")
	}
}
