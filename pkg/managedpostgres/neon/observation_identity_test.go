// adr: 463 — health observes the pinned dataset after default-branch drift.
package neon

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"net/http"
	"testing"
	"time"
)

func TestHealthStaysOnPinnedOriginalAfterDefaultDrift(t *testing.T) {
	const project = "quiet-river-12345678"
	const original = project + "/br-original-123"
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation: %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v2/projects/" + project:
			writeResponse(t, w, 200, map[string]any{"project": map[string]any{"id": project, "region_id": "aws-eu-central-1", "pg_version": 17, "history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": 10 << 30}}}})
		case "/api/v2/projects/" + project + "/branches":
			writeResponse(t, w, 200, map[string]any{"branches": []map[string]any{{"id": "br-original-123", "default": false, "current_state": "ready"}, {"id": "br-copy-123", "default": true, "current_state": "ready"}}})
		case "/api/v2/projects/" + project + "/endpoints":
			// The original branch has lost compute; only the copy is healthy.
			writeResponse(t, w, 200, map[string]any{"endpoints": []map[string]any{{"id": "ep-copy-123", "branch_id": "br-copy-123", "type": "read_write", "current_state": "active", "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 2, "suspend_timeout_seconds": 300}}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	pinned, err := p.Observe(t.Context(), original)
	if err != nil || pinned.Status != managedpostgres.ProviderStatusFailed {
		t.Fatalf("pinned branch should be failed: %+v %v", pinned, err)
	}
	backend := testBackend()
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: backend.Region, Defaults: map[string]string{backend.Region: backend.ID}, MaxDatabasesPerAccount: 2, Backends: []managedpostgres.BackendConfig{backend}}, func(string) string { return "fixture" }, map[string]managedpostgres.Factory{"neon": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return p, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Default(backend.Region)
	if err != nil {
		t.Fatal(err)
	}
	store := managedpostgres.NewMemoryStore()
	now := time.Now().UTC()
	ctx := context.Background()
	database, _, err := store.Reserve(ctx, managedpostgres.Database{ID: uuid.NewString(), AccountID: "account", Name: "orders", State: managedpostgres.StateProvisioning, Spec: testDatabaseSpec(), BackendID: resolved.ID, BackendFingerprint: resolved.Fingerprint, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}, 2)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, database.AccountID, database.ID, "provision", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(ctx, database.ID, claim.LeaseToken, project, now); err != nil {
		t.Fatal(err)
	}
	claim.ProviderResourceID = project
	database, err = store.FinishProvisionWithDataResource(ctx, claim, managedpostgres.ObservedDatabase{ProviderResourceID: project, DataResourceID: original, Status: managedpostgres.ProviderStatusReady, Spec: database.Spec}, now)
	if err != nil {
		t.Fatal(err)
	}
	collector, err := managedpostgres.NewHealthCollector(registry, store, managedpostgres.HealthCollectorOptions{Now: func() time.Time { return now }, MinimumSpacing: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if summary, err := collector.Sweep(ctx); err != nil || summary.Checked != 1 {
		t.Fatalf("sweep %+v %v", summary, err)
	}
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, database.AccountID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pinned branch status=%s; API health=%+v", pinned.Status, got.Health)
	if got.Health == nil || got.Health.Status != "degraded" || got.Health.LastErrorCode != "provider_failed" {
		t.Fatal("health reported the unavailable original as healthy using the default copy")
	}
}
