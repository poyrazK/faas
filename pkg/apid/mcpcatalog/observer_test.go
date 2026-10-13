package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagedCatalogObserverMemory(t *testing.T) {
	testManagedCatalogObserver(t, state.NewMemStore())
}
func TestManagedCatalogObserverPostgres(t *testing.T) {
	testManagedCatalogObserver(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testManagedCatalogObserver(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "managed-catalog@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "managed-catalog", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{{Name: "reviewed", InputSchema: map[string]any{"type": "object"}}}})
	raw, _ := mcphosting.MarshalContract(baseline)
	config := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), Baseline: raw, BaselineSHA256: digest(raw), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", SecretRef: "secret:READER_TOKEN"}}
	monitors := store.(state.MCPCatalogMonitorStore)
	observers := store.(state.MCPCatalogObserverStore)
	if _, err := monitors.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, config); err != nil {
		t.Fatal(err)
	}
	jobs, err := observers.ListDueMCPCatalogObservers(ctx, 4)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("due: %d %v", len(jobs), err)
	}
	job, err := observers.ClaimMCPCatalogObserver(ctx, jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observers.ClaimMCPCatalogObserver(ctx, jobs[0]); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("duplicate live lease acquired")
	}
	if due, err := observers.ListDueMCPCatalogObservers(ctx, 4); err != nil || len(due) != 0 {
		t.Fatal("leased role remained due")
	}
	fakeReceipt := api.MCPCatalogReceipt{Role: "reader", ID: uuid.NewString(), Revision: config.Revision, State: "healthy", ObservedSHA256: digest(raw)}
	forged := job
	forged.LeaseID = uuid.NewString()
	if _, err := observers.CompleteMCPCatalogObserver(ctx, forged, fakeReceipt, "", 30*time.Second); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("wrong fence accepted")
	}
	if _, err := monitors.RecordMCPCatalogObservation(ctx, account.ID, app.ID, fakeReceipt); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("manual publisher bypassed managed lease")
	}
	if _, err := observers.CompleteMCPCatalogObserver(ctx, job, fakeReceipt, "", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := observers.CompleteMCPCatalogObserver(ctx, job, fakeReceipt, "", 30*time.Second); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("completed lease replayed")
	}
	identity, _ := age.GenerateX25519Identity()
	putToken := func(value string) {
		sealed, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"READER_TOKEN": value})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppSecret(ctx, account.ID, app.ID, "READER_TOKEN", sealed); err != nil {
			t.Fatal(err)
		}
	}
	putToken("FIRST_PRIVATE_TOKEN")
	tokens := []string{}
	mu := sync.Mutex{}
	runner := ObserverRunner{Store: store, Domain: "apps.example.com", Identities: func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }, Probe: func(_ context.Context, endpoint, token, version string) ([]byte, error) {
		if endpoint != "https://managed-catalog.apps.example.com/mcp" || version != mcphosting.ProtocolVersion {
			t.Error("wrong app origin or protocol")
		}
		mu.Lock()
		tokens = append(tokens, token)
		mu.Unlock()
		return raw, nil
	}}
	restart := func() {
		config.Revision = uuid.NewString()
		if _, err := monitors.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, config); err != nil {
			t.Fatal(err)
		}
	}
	restart()
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ := monitors.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	view := root.Roles["reader"]
	if view.Latest.State != "healthy" || view.ObserverStatus.LastSuccessAt == nil || view.ObserverStatus.NextCheckAt == nil || view.ObserverStatus.LeaseID != "" {
		t.Fatal("successful check not scheduled or retained")
	}
	// Running twice before next_check_at must not probe again.
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatal("configured cadence bypassed")
	}
	putToken("ROTATED_PRIVATE_TOKEN")
	restart()
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 || tokens[0] != "FIRST_PRIVATE_TOKEN" || tokens[1] != "ROTATED_PRIVATE_TOKEN" {
		t.Fatal("secret rotation not read on the next check")
	}
	// Reconfiguration makes the old lease ineligible even when the old probe succeeded.
	restart()
	jobs, _ = observers.ListDueMCPCatalogObservers(ctx, 4)
	old, _ := observers.ClaimMCPCatalogObserver(ctx, jobs[0])
	restart()
	fakeReceipt.Revision = old.Snapshot.Config.Revision
	if _, err := observers.CompleteMCPCatalogObserver(ctx, old, fakeReceipt, "", 30*time.Second); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("reconfigured observer accepted old receipt")
	}
	runner.Probe = func(context.Context, string, string, string) ([]byte, error) {
		return nil, errors.New("PRIVATE_SOURCE_ERROR")
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ = monitors.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	view = root.Roles["reader"]
	if view.Latest.State != "unavailable" || view.ObserverStatus.FailureReason != "source_unavailable" || view.ObserverStatus.ConsecutiveFailures != 1 {
		t.Fatal("failed source did not schedule a degraded retry")
	}
	if view.ObserverStatus.NextCheckAt.Sub(*view.CheckedAt) < 60*time.Second {
		t.Fatal("retry backoff missing")
	}
	data, _ := json.Marshal(root)
	if strings.Contains(string(data), "PRIVATE_") {
		t.Fatal("private credential or error retained")
	}
	config.Enabled = false
	config.Observer.Paused = true
	restart()
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ = monitors.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	view = root.Roles["reader"]
	if api.CatalogObserverStatus(view, time.Now().UTC()).State != "paused" || len(tokens) != 2 {
		t.Fatal("paused role probed")
	}
	config.Enabled = true
	config.Observer.Paused = false
	restart()
	if err := store.DeleteAppSecret(ctx, account.ID, app.ID, "READER_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	root, _ = monitors.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
	if root.Roles["reader"].ObserverStatus.FailureReason != "credential_unavailable" {
		t.Fatal("missing secret silently became anonymous")
	}
	other, err := store.CreateAccount(ctx, "other-managed@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	cross := old
	cross.AccountID = other.ID
	if _, err := observers.ClaimMCPCatalogObserver(ctx, cross); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account lease accepted")
	}
}
func TestManagedCatalogObserverExpiredLease(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	account, _ := store.CreateAccount(ctx, "expired-observer@example.com", api.PlanHobby)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "expired-observer", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	config := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 10, Endpoint: "/mcp"}}
	if _, err := store.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, config); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListDueMCPCatalogObservers(ctx, 4)
	old, err := store.ClaimMCPCatalogObserver(ctx, jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE mcp_catalog_monitors SET payload=jsonb_set(payload,'{roles,reader,observer_status,lease_until}',to_jsonb(now()-interval '1 minute')) WHERE app_id=$1::uuid`, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = store.ListDueMCPCatalogObservers(ctx, 4)
	if err != nil || len(jobs) != 1 {
		t.Fatal("expired lease was not recovered")
	}
	renewed, err := store.ClaimMCPCatalogObserver(ctx, jobs[0])
	if err != nil || renewed.LeaseID == old.LeaseID {
		t.Fatal("recovered lease reused fencing token")
	}
	receipt := api.MCPCatalogReceipt{Role: "reader", ID: uuid.NewString(), Revision: config.Revision, State: "unavailable"}
	if _, err := store.CompleteMCPCatalogObserver(ctx, old, receipt, "source_unavailable", time.Minute); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("expired worker overwrote replacement")
	}
	if _, err := store.CompleteMCPCatalogObserver(ctx, renewed, receipt, "source_unavailable", time.Minute); err != nil {
		t.Fatal(err)
	}
}
func TestManagedObserverDelayAndOrigin(t *testing.T) {
	for _, domain := range []string{"example.com/path", "user@example.com", "example.com:443", "example.com?token=x"} {
		if _, err := managedObserverEndpoint(domain, "app", "/mcp"); err == nil {
			t.Fatal("unsafe app origin accepted")
		}
	}
	for failures := 0; failures <= api.MCPCatalogObserverMaxFailures; failures++ {
		delay := observerDelay(30, failures)
		if delay < 30*time.Second || delay > 660*time.Second {
			t.Fatal("retry delay outside bounds")
		}
	}
}
