// adr: 20261016151947; 20261025094915 — live authorization and storage use dataset pins.
package neon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func datasetTestRegistry(t *testing.T, p *Provider) (*managedpostgres.Registry, managedpostgres.Backend) {
	t.Helper()
	cfg := testBackend()
	cfg.Settings[settingDatabaseName] = p.databaseName
	cfg.LiveHubURL = "wss://hub.test/{database_id}"
	r, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: cfg.Region, Defaults: map[string]string{cfg.Region: cfg.ID}, Backends: []managedpostgres.BackendConfig{cfg}, MaxDatabasesPerAccount: 10}, func(string) string { return "fixture" }, map[string]managedpostgres.Factory{"neon": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
		return p, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Default(cfg.Region)
	if err != nil {
		t.Fatal(err)
	}
	return r, b
}

func datasetTestReady(t *testing.T, store *managedpostgres.MemoryStore, b managedpostgres.Backend, name, provider, pin string, now time.Time) managedpostgres.Database {
	t.Helper()
	d, _, err := store.Reserve(t.Context(), managedpostgres.Database{ID: uuid.NewString(), AccountID: "account-a", Name: name, State: managedpostgres.StateProvisioning, Spec: testDatabaseSpec(), BackendID: b.ID, BackendFingerprint: b.Fingerprint, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now}, 10)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(t.Context(), d.AccountID, d.ID, "provision", managedpostgres.StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordProviderResource(t.Context(), d.ID, claim.LeaseToken, provider, now); err != nil {
		t.Fatal(err)
	}
	claim.ProviderResourceID = provider
	d, err = store.FinishProvisionWithDataResource(t.Context(), claim, managedpostgres.ObservedDatabase{ProviderResourceID: provider, DataResourceID: pin, Status: managedpostgres.ProviderStatusReady, Spec: d.Spec}, now)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLiveLoginCanAssumePinnedBindingRoles(t *testing.T) {
	f := newCredentialFixture(t)
	const project = "quiet-river-12345678"
	const pin = project + "/br-original-123"
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/projects/" + project + "/branches":
			writeResponse(t, w, 200, map[string]any{"branches": []map[string]any{{"id": "br-original-123", "default": true, "current_state": "ready"}}})
		case "/api/v2/projects/" + project + "/connection_uri":
			if r.URL.Query().Get("branch_id") != "br-original-123" {
				t.Error("wrong physical branch")
			}
			writeResponse(t, w, 200, map[string]any{"uri": "postgres://" + ownerLogin + ":test@test.invalid/" + f.config.Database + "?sslmode=require"})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	p.databaseName = f.config.Database
	p.roles = f.manager
	p.liveLogins = &sqlLiveLogins{connect: f.manager.connect}
	reg, b := datasetTestRegistry(t, p)
	store := managedpostgres.NewMemoryStore()
	d := datasetTestReady(t, store, b, "orders", project, pin, time.Now().UTC())
	// Ordinary bindings use the persisted dataset selector, as BindingService does.
	req := managedpostgres.CredentialRequest{ProviderResourceID: d.DataResourceID, IdentityKey: "existing-binding", Access: managedpostgres.CredentialReadWrite}
	f.runtime = p.credentialRole(req)
	if err := f.manager.Ensure(t.Context(), f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(reg, store, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	live, ok, err := svc.IssueLiveLogin(t.Context(), d, "gregale_live_test_slot")
	if err != nil || !ok {
		t.Fatalf("enable live login: %t %v", ok, err)
	}
	f.extraRoles = append(f.extraRoles, live.ProviderIdentityID)
	req.IdentityKey = "later-binding"
	later := p.credentialRole(req)
	f.extraRoles = append(f.extraRoles, later.name)
	if err := f.manager.Ensure(t.Context(), f.material, later); err != nil {
		t.Fatal(err)
	}
	var existingSet, laterSet bool
	if err := f.admin.QueryRow(t.Context(), `SELECT pg_has_role($1,$2,'SET'), pg_has_role($1,$3,'SET')`, live.ProviderIdentityID, f.runtime.name, later.name).Scan(&existingSet, &laterSet); err != nil {
		t.Fatal(err)
	}
	conn := f.connect(t, live.ProviderIdentityID)
	_, roleErr := conn.Exec(t.Context(), "SET ROLE "+roleIdentifier(f.runtime.name))
	t.Logf("live enable succeeded on the original branch; can SET existing binding=%t, later binding=%t; actual SET ROLE error=%v", existingSet, laterSet, roleErr)
	if !existingSet || !laterSet || roleErr != nil {
		t.Fatal("project-scoped live login cannot assume normal branch-scoped binding roles")
	}
}

func TestStoragePoolUsesPinnedBranchSizes(t *testing.T) {
	const a = "quiet-river-12345678"
	const b = "calm-ocean-12345678"
	const mib = int64(1 << 20)
	var mu sync.Mutex
	limits := map[string]int64{a: 1000 * mib, b: 1000 * mib}
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		project := a
		if strings.HasPrefix(r.URL.Path, "/api/v2/projects/"+b) {
			project = b
		}
		base := "/api/v2/projects/" + project
		switch {
		case r.Method == http.MethodGet && r.URL.Path == base:
			writeResponse(t, w, 200, map[string]any{"project": map[string]any{"id": project, "settings": map[string]any{"quota": map[string]any{"compute_time_seconds": 10000, "logical_size_bytes": limits[project]}}}})
		case r.Method == http.MethodPatch && r.URL.Path == base:
			var patch computeCapPatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Error(err)
			}
			if patch.Project.Settings.Quota.LogicalSizeBytes == nil {
				t.Error("no storage quota")
			} else {
				limits[project] = *patch.Project.Settings.Quota.LogicalSizeBytes
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == base+"/branches":
			branches := []map[string]any{{"id": "br-original-123", "default": true, "logical_size": 100 * mib}}
			if project == a {
				branches = []map[string]any{{"id": "br-original-123", "default": false, "logical_size": 900 * mib}, {"id": "br-copy-123", "default": true, "logical_size": 100 * mib}}
			}
			writeResponse(t, w, 200, map[string]any{"branches": branches})
		case r.Method == http.MethodGet && r.URL.Path == base+"/branches/br-original-123":
			size := 100 * mib
			if project == a {
				size = 900 * mib
			}
			writeResponse(t, w, 200, map[string]any{"branch": map[string]any{"id": "br-original-123", "logical_size": size}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	reg, backend := datasetTestRegistry(t, p)
	store := managedpostgres.NewMemoryStore()
	now := time.Now().UTC()
	datasetTestReady(t, store, backend, "large-original", a, a+"/br-original-123", now)
	datasetTestReady(t, store, backend, "small-independent", b, b+"/br-original-123", now.Add(time.Microsecond))
	actual, err := p.ObserveStorageCap(t.Context(), a+"/br-original-123")
	if err != nil || actual.SizeBytes != 900*mib {
		t.Fatalf("pinned size fixture: %+v %v", actual, err)
	}
	qualification := &managedpostgres.StorageCapQualification{Version: managedpostgres.StorageCapQualificationVersion, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, CompletedAt: now, Evidence: managedpostgres.StorageCapEvidence{SizeReported: true, WritesRefusedAtLimit: true, ReadsAllowedAtLimit: true, ComputeKeptRunning: true, WritesAllowedAfterRaise: true, ComputeQuotaPreserved: true}, Checks: []managedpostgres.QualificationCheck{{Name: "storage_cap_probe", Passed: true}}}
	enforcer, err := managedpostgres.NewCapEnforcer(reg, store, managedpostgres.CapEnforcerOptions{Mode: managedpostgres.CapEnforcementOff, StorageMode: managedpostgres.CapEnforcementOn, StorageQualification: qualification, Now: func() time.Time { return now }, Ceiling: func(context.Context, string) (managedpostgres.CapLimits, error) {
		return managedpostgres.CapLimits{StoragePool: 1000 * mib}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := enforcer.Enforce(t.Context())
	if err != nil || summary.StorageFailed != 0 {
		t.Fatalf("enforcement failed instead of undercounting: %+v %v", summary, err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("actual pool use=1000 MiB; blocked accounts=%d; small independent database quota=%d MiB", summary.StorageBlockedAccounts, limits[b]/mib)
	if summary.StorageBlockedAccounts != 1 || limits[b] > 100*mib {
		t.Fatal("default-branch drift invented storage headroom in an already-full pool")
	}
}
