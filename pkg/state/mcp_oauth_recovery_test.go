package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOAuthRenewalRetryPreservesJournalAndFences(t *testing.T) {
	at := time.Now().UTC()
	makeRoot := func() api.MCPCatalogMonitorSnapshot {
		return api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: "revision", Enabled: true, Observer: &api.MCPCatalogObserverConfig{Enabled: true, OAuth: &api.MCPCatalogObserverOAuthConfig{RefreshTokenSecretRef: "secret:REFRESH"}}}, ObserverStatus: &api.MCPCatalogObserverStatus{State: "degraded", FailureReason: "credential_unavailable", Renewal: &api.MCPCatalogOAuthRenewal{ID: "operation", State: "failed"}}}
	}
	root := makeRoot()
	out, err := state.RetryCatalogOAuthRenewal(&root, "default", "revision", at)
	if err != nil || out.State != "renewal_scheduled" || out.Revision == "revision" || root.ObserverStatus.NextCheckAt == nil || !root.ObserverStatus.NextCheckAt.Equal(at) || root.ObserverStatus.Renewal.ID != "operation" {
		t.Fatal("retry did not preserve and schedule", out, err)
	}
	if _, err = state.RetryCatalogOAuthRenewal(&root, "default", "revision", at); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("stale revision replayed", err)
	}
	for _, scenario := range []string{"pending", "unknown", "lease", "paused", "saved-uncertain", "reauthorization", "browser-flow"} {
		t.Run(scenario, func(t *testing.T) {
			root := makeRoot()
			until := at.Add(time.Minute)
			switch scenario {
			case "pending":
				root.ObserverStatus.Renewal.State = "pending"
			case "unknown":
				root.ObserverStatus.Renewal.Outcome = "issuance_unknown"
			case "lease":
				root.ObserverStatus.LeaseUntil = &until
			case "paused":
				root.Config.Observer.Paused = true
			case "saved-uncertain":
				root.ObserverStatus.Renewal.State = "saved"
				root.ObserverStatus.Renewal.Result = &api.MCPCatalogObserverOAuthStatus{State: "reauthorization_required"}
			case "reauthorization":
				root.ObserverStatus.OAuth = &api.MCPCatalogObserverOAuthStatus{State: "reauthorization_required"}
			case "browser-flow":
				root.ObserverStatus.OAuthFlow = &api.MCPCatalogOAuthFlow{ExpiresAt: until}
			}
			if _, err := state.RetryCatalogOAuthRenewal(&root, "default", "revision", at); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unsafe retry allowed", err)
			}
			if root.Config.Revision != "revision" {
				t.Fatal("refusal mutated revision")
			}
		})
	}
	root = makeRoot()
	saved := &api.MCPCatalogObserverOAuthStatus{State: "connected", Ciphertext: []byte("sealed")}
	root.ObserverStatus.Renewal = &api.MCPCatalogOAuthRenewal{ID: "saved-operation", State: "saved", Outcome: "response_recorded", Result: saved}
	root.ObserverStatus.OAuth = &api.MCPCatalogObserverOAuthStatus{State: "reauthorization_required"}
	if _, err := state.RetryCatalogOAuthRenewal(&root, "default", "revision", at); err != nil || root.ObserverStatus.Renewal.Result != saved {
		t.Fatal("saved response was not preserved for installation", err)
	}
}

func TestMCPOAuthRecoveryStoreMemory(t *testing.T) { testMCPOAuthRecoveryStore(t, state.NewMemStore()) }
func TestMCPOAuthRecoveryStorePostgres(t *testing.T) {
	testMCPOAuthRecoveryStore(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testMCPOAuthRecoveryStore(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "renewal-recovery@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "renewal-recovery", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, Endpoint: "/mcp", IntervalSeconds: 30, TimeoutSeconds: 1, OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	monitors := store.(state.MCPCatalogMonitorStore)
	view, err := monitors.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	observers := store.(state.MCPCatalogObserverStore)
	job, err := observers.ClaimMCPCatalogObserver(ctx, state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: "reader", Snapshot: view})
	if err != nil {
		t.Fatal(err)
	}
	renewals := store.(state.MCPCatalogOAuthRenewalStore)
	journal, fresh, err := renewals.BeginMCPCatalogOAuthRenewal(ctx, job, strings.Repeat("a", 64))
	if err != nil || !fresh {
		t.Fatal("renewal intent", err, fresh)
	}
	if err = renewals.RecordMCPCatalogOAuthRenewal(ctx, job, journal.ID, "failed", nil); err != nil {
		t.Fatal(err)
	}
	_, err = observers.CompleteMCPCatalogObserver(ctx, job, api.MCPCatalogReceipt{ID: uuid.NewString(), Role: "reader", Revision: view.Config.Revision, State: "unavailable"}, "credential_unavailable", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	source := store.(state.MCPCatalogOAuthRecoveryStore)
	other, err := store.CreateAccount(ctx, "renewal-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.RetryMCPCatalogOAuthRenewal(ctx, other.ID, app.ID, "reader", view.Config.Revision); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account queued renewal", err)
	}
	out, err := source.RetryMCPCatalogOAuthRenewal(ctx, account.ID, app.ID, "reader", view.Config.Revision)
	if err != nil || out.State != "renewal_scheduled" {
		t.Fatal("store did not queue recovery", out, err)
	}
	if _, err = source.RetryMCPCatalogOAuthRenewal(ctx, account.ID, app.ID, "reader", view.Config.Revision); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("stale store retry allowed", err)
	}
	root, err := monitors.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if recovered.ObserverStatus.NextCheckAt == nil || recovered.ObserverStatus.Renewal == nil || recovered.ObserverStatus.Renewal.State != "failed" || recovered.Config.Revision != out.Revision {
		t.Fatal("durable scheduling lost journal", recovered)
	}
}
