package managedpostgres

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestConnectionBudgetPGReadsSharedScopesOverlapAndIsolation(t *testing.T) {
	s, pool, ctx, accountID := postgresStoreFixture(t)
	ps := state.NewPgStore(pool)
	database := postgresReadyDatabase(t, s, accountID, "budget-primary", time.Now().UTC())
	app, err := ps.CreateApp(ctx, state.App{AccountID: accountID, Slug: "budget-" + uuid.NewString()[:8], MaxConcurrency: 3, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := ps.CreateComputeNode(ctx, state.ComputeNode{Name: "budget-" + uuid.NewString(), TargetURL: "unix:///tmp/budget.sock", VPCPUs: 2, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []drainPlanInstanceSpec{{"default", "normal", "running"}, {"default", "normal", "waking"}, {"default", "normal", "draining"}, {"default", "normal", "parked"}, {"default", "mirror", "running"}, {"other", "normal", "running"}} {
		d, err := ps.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: spec.scope, Kind: state.DeploymentKindImage, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ps.CreateInstanceWithMode(ctx, app.ID, d.ID, spec.lifecycle, 256, node.ID, "", spec.mode); err != nil {
			t.Fatal(err)
		}
	}
	binding := testBinding(accountID, database.ID, app.ID, uuid.NewString(), time.Now().UTC())
	binding.Access = CredentialDataAPI
	binding.Scope = "default"
	if _, _, err = s.ReserveBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	service := &BindingService{databases: s, bindings: s, now: func() time.Time { return time.Now().UTC() }}
	got, err := service.ConnectionBudget(t.Context(), accountID, database.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "unknown" || got.Inventory.ResidentInstances != 3 || got.Inventory.ParkedInstances != 1 || got.Inventory.AuxiliaryInstances != 1 || got.Demand.DataAPIResidentEstimate != 6 || got.Demand.DataAPIWakeEstimate != 8 || !slices.Contains(got.Reasons, "capacity_unverified") {
		t.Fatal(got)
	}
	if _, err = service.ConnectionBudget(t.Context(), uuid.NewString(), database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account read", err)
	}
	list, err := service.AppConnectionBudgets(t.Context(), accountID, app.ID, app.Slug)
	if err != nil || len(list.Databases) == 0 || list.State != "unknown" {
		t.Fatal(list, err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE compute_nodes SET last_heartbeat_at=now()-interval '1 hour' WHERE id=$1`, node.ID); err != nil {
		t.Fatal(err)
	}
	got, err = service.ConnectionBudget(t.Context(), accountID, database.ID)
	if err != nil || got.Inventory.StaleNodeInstances < 5 || !slices.Contains(got.Reasons, "node_observations_stale") {
		t.Fatal(got, err)
	}
	// Replacing the catalog adapter preserves the same read-only accounting.
	restarted, _ := NewPostgresStore(s.pool)
	if _, err = restarted.ReadConnectionBudget(t.Context(), accountID, database.ID); err != nil {
		t.Fatal(err)
	}
	bytes, _ := json.Marshal(got)
	for _, secret := range []string{app.ID, node.ID, "credential_ref", "provider_resource_id", "backend_fingerprint"} {
		if strings.Contains(string(bytes), secret) {
			t.Fatal("private metadata escaped")
		}
	}
}
