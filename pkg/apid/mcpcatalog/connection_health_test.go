package mcpcatalog

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestConnectionHealthPostgresDeletedRetirementAndIsolation(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@health.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "health-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}}}
	if _, err = store.ConfigureMCPCatalogMonitor(ctx, acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	health, err := store.MCPConnectionHealth(ctx, acct.ID, app.ID, 0, now)
	if err != nil || !health.Complete || health.Counts["reauthorization_required"] != 1 {
		t.Fatal("PostgreSQL health projection failed", err)
	}
	flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("PRIVATE_VERIFIER"), ExpiresAt: now.Add(time.Minute)}
	view, err := store.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", cfg.Revision, "begin", "", &flow, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err = store.ChangeMCPCatalogOAuth(ctx, acct.ID, app.ID, "reader", view.Config.Revision, "consume", flow.StateSHA256, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PrepareMCPOAuthRetirement(ctx, state.MCPCatalogObserverJob{AccountID: acct.ID, AppID: app.ID, Slug: app.Slug, Role: "reader", Snapshot: view}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	health, err = store.MCPConnectionHealth(ctx, acct.ID, "", 0, now.Add(6*24*time.Hour+2*time.Hour))
	if err != nil || health.Counts["issuance_unknown"] != 1 || health.Counts["retirement_expiring"] != 1 || health.Counts["reauthorization_required"] != 0 {
		t.Fatal("deleted app retirement omitted", err)
	}
	for _, issue := range health.Items {
		if !issue.AppDeleted || issue.RetirementID == "" || issue.RecoveryPath == "" {
			t.Fatal("deleted retirement lost attribution or recovery")
		}
	}
	encoded, _ := json.Marshal(health)
	for _, secret := range []string{"private_", "ciphertext", "PRIVATE", "lease_id", "source_sha256"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("PostgreSQL health exposed private data")
		}
	}
	other, err := store.CreateAccount(ctx, uuid.NewString()+"@health.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := store.MCPConnectionHealth(ctx, other.ID, "", 0, now)
	if err != nil || isolated.TotalIssues != 0 {
		t.Fatal("health leaked cross-account metadata", err)
	}
}
