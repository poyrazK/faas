package db_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrivateNetworkDetachIsDurableBeforeDeleteCommits(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := state.NewPgStore(pool)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	account, err := store.CreateAccount(ctx, "private-detach@example.com", api.PlanScale)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "private-detach"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: account.ID,
		AppID:     app.ID,
		NetworkID: "vpc-1",
		Region:    "fra1",
		CIDRs:     []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
		Status:    api.PrivateNetworkAttachmentStatusReady,
	}); err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	if err := store.DeleteAppPrivateNetworkAttachment(ctx, account.ID, app.ID); err != nil {
		t.Fatalf("delete attachment: %v", err)
	}

	var payload string
	if err := pool.QueryRow(ctx, `
		SELECT payload
		  FROM notification_outbox
		 WHERE channel = $1
		   AND payload::jsonb ->> 'app_id' = $2
		 ORDER BY id DESC
		 LIMIT 1`, db.NotifyPrivateNetworkAttachmentChanged, app.ID).Scan(&payload); err != nil {
		t.Fatalf("read durable detach event: %v", err)
	}
	parsed, err := db.ParseAppChangedPayload(payload)
	if err != nil {
		t.Fatalf("parse durable detach payload: %v", err)
	}
	if parsed.Kind != "private_network_attachment" || parsed.Status != "detached" || parsed.AppID != app.ID {
		t.Fatalf("durable detach payload = %+v, want app=%s detached attachment", parsed, app.ID)
	}
}
