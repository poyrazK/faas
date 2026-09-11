// Package e2e — egress metering smoke (ADR-046, step 11).
//
// CI-safe non-metal test: seeds a deployment + parked instance, plants
// non-zero tx_bytes + net_tx_bytes via AppendUsage, calls GET /v1/usage,
// and asserts the response carries both fields with the seeded values.
// This is the e2e analogue of pkg/state's
// TestPg_AppendUsage_AddsTxBytesAndNetTxBytesOnConflict — the difference
// is that this test goes through the apid HTTP surface (read-back only),
// proving the apid → store → UsageResponse wiring is intact end-to-end.
//
// The gateway-side tx_bytes producer (cmd/gatewayd statusRecorder) and
// the vmmd-side net_tx_bytes poller land in PR-2; this test does not
// boot them. The PR-1 contract is "the seam exists in the schema, in the
// store, and in the API surface; the values travel when the producers
// write them." This test pins the read side of that contract.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

//go:build !no_pg

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEgressMetering_GETUsage_SurfacesTxAndNetTxBytes plants
// non-zero tx_bytes (gateway-side HTTP response bytes) and
// net_tx_bytes (root-side vethHost.rx_bytes delta) on a parked
// instance via the state.Store.AppendUsage path. GET /v1/usage
// for the account must surface both fields with the seeded
// values in the per-app UsageResponse row.
//
// This is the read-back counterpart to the persistence test
// in pkg/state/pgstore_append_usage_test.go — the test asserts
// the values traverse apid → UsageByMonth → UsageResponse
// without dropping either column.
func TestEgressMetering_GETUsage_SurfacesTxAndNetTxBytes(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	// Slot 65 is the egress migration; pin it so a future
	// PR that reorders migrations doesn't silently re-run
	// this test against an older schema.
	pgtest.WaitForMigration(t, pool, 65, 10*time.Second)

	// Only apid is needed — this is a pure read-back test. Schedd
	// and meterd would write through the new columns in production,
	// but the test seeds the values directly via AppendUsage so it
	// stays CI-safe (no KVM, no meterd tick).
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)

	store := state.NewPgStore(pool)

	// Resolve the default-local compute_node id once at boot so
	// the FK on instances.node_id has a real target.
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("resolve default-local compute_node: %v", err)
	}

	// Seed the account via the apid's seed path so the bearer
	// token has the right scopes (usage:read).
	acctEmail := "egress-e2e@" + uuid.NewString() + ".test.example"
	acct, err := store.CreateAccount(ctx, acctEmail, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e", api.ScopesUsageReadSurface); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "egress-e2e",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// Parked so the meterd quota loop never appends mb_seconds
	// on top of our seed (spec invariant §6.2-4).
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, node.ID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Plant the egress values for the current minute. Use a
	// timestamp 1 minute ago so UsageByMonth (which sums
	// usage_minutes by date_trunc('month', minute)) always
	// returns our row regardless of wall-clock skew between the
	// test's clock and Postgres's clock.
	minute := time.Now().UTC().Add(-time.Minute).Truncate(time.Minute)
	if err := store.AppendUsage(ctx, acct.ID, app.ID, ins.ID,
		minute, 0, 0, 0, 1_000_000, 4_000_000, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}

	// GET /v1/usage for the seeded month. apid's getUsage
	// parses the ?month= query (defaults to current month) and
	// returns the per-app UsageResponse slice.
	month := minute.Format("2006-01")
	url := h.APIDURL + "/v1/usage?month=" + month
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+pt)

	rec, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer rec.Body.Close()
	if rec.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/usage: status = %d, want 200", rec.StatusCode)
	}

	var rows []api.UsageResponse
	if err := json.NewDecoder(rec.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].AppID != app.ID {
		t.Errorf("AppID = %q, want %q", rows[0].AppID, app.ID)
	}
	// TXBytes is the gateway-side HTTP response byte count.
	if rows[0].TXBytes != 1_000_000 {
		t.Errorf("tx_bytes = %d, want 1_000_000", rows[0].TXBytes)
	}
	// NetTxBytes is the root-side vethHost.rx_bytes delta.
	if rows[0].NetTxBytes != 4_000_000 {
		t.Errorf("net_tx_bytes = %d, want 4_000_000", rows[0].NetTxBytes)
	}
	// TotalEgressGB() is the informational byte→GB conversion the
	// dashboard uses to render the egress column. Pin it so a
	// future DTO drift breaks the test, not the dashboard. NOTE
	// (ADR-046 PR-414 I5): the value INCLUDES Ethernet framing
	// because net_tx_bytes is the root-side kernel interface
	// counter. Future billing will pick the unit; this is a
	// dashboard-only surface today.
	gotGB := rows[0].TotalEgressGB()
	wantBytes := float64(4_000_000) / (1024 * 1024 * 1024)
	if diff := gotGB - wantBytes; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("TotalEgressGB = %g, want %g", gotGB, wantBytes)
	}
}
