// traffic_mirror_e2e_test.go — issue #72 / ADR-124 / ADR-125 PR-A3
// commit 5
//
// E2E coverage for the runtime half of traffic mirroring: the
// gateway dispatch goroutine + schedd admission + the rollup +
// the per-rule slot cap. Pins three load-bearing contracts via
// Postgres and the KVM-free general path:
//
//  1. TestE2E_MirrorDispatch_HappyPath
//     - boot the real apid + schedd + gatewayd stack
//     - create live source/mirror deployments and a rule through
//       the public API (including pg_notify cache refresh)
//     - fire one customer request through gatewayd
//     - assert schedd admits a new mirror instance with
//       mode='mirror', without changing the source response
//
//  2. TestE2E_MirrorRollup_AggregatesByRuleHour
//     - write 5 mirror_invocation_results rows in the last hour
//       for a seeded rule
//     - run mirror.RollupOnce
//     - assert mirror_invocation_summary has one row for the
//       rule with total_invocations = 5
//
//  3. TestE2E_MirrorSweep_DeletesOnlyStaleRows
//     - write 3 rows aged 8d + 2 rows aged 1d
//     - run mirror.SweepOldLedgerRows with a 7d cutoff
//     - assert only the 2 fresh rows remain
//
// Build tag: !no_pg. CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS). Runs under `make test-pg`.

//go:build !no_pg

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	mirrorRollup "github.com/onebox-faas/faas/pkg/mirror"
	"github.com/onebox-faas/faas/pkg/state"
	artifactstorage "github.com/onebox-faas/faas/pkg/storage"
)

// seedMirrorFixture inserts the parent rows the mirror_invocation_results
// FK chain needs: an account, an app, two deployments (one source, one
// mirror) that point at the seeded mirror_rules row, and the mirror_rule
// itself. Returns the (accountID, sourceDeploymentID, mirrorDeploymentID)
// so the row INSERT can populate the NOT NULL mirror_invocation_results
// columns consistently.
//
// All five parent rows live or die together with the test — there is no
// cleanup; the per-test pgtest schema is dropped on test exit so the
// next test gets a fresh slate.
func seedMirrorFixture(t *testing.T, pool *pgxpool.Pool, appID string) (accountID, sourceDeploymentID, mirrorDeploymentID, ruleID string) {
	t.Helper()
	accountID = uuid.NewString()
	sourceDeploymentID = uuid.NewString()
	mirrorDeploymentID = uuid.NewString()
	ruleID = uuid.NewString()
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
INSERT INTO accounts (id, email, plan, created_at)
VALUES ($1::uuid, $2, 'free', now())
ON CONFLICT (id) DO NOTHING
`, accountID, accountID+"@e2e.invalid"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO apps (id, account_id, slug, ram_mb, max_concurrency, status, created_at)
VALUES ($1::uuid, $2::uuid, $3, 128, 1, 'active', now())
ON CONFLICT (id) DO NOTHING
`, appID, accountID, "mirror-e2e-"+appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO deployments (id, app_id, scope, image_digest, status, created_at)
VALUES ($1::uuid, $2::uuid, 'mirror-source', 'sha256:e2e-source-' || $1::uuid, 'live', now())
ON CONFLICT (id) DO NOTHING
`, sourceDeploymentID, appID); err != nil {
		t.Fatalf("seed source deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO deployments (id, app_id, scope, image_digest, status, created_at)
VALUES ($1::uuid, $2::uuid, 'mirror-target', 'sha256:e2e-mirror-' || $1::uuid, 'live', now())
ON CONFLICT (id) DO NOTHING
`, mirrorDeploymentID, appID); err != nil {
		t.Fatalf("seed mirror deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO mirror_rules (
    id, account_id, app_id, source_deployment_id, mirror_deployment_id,
    percent, enabled, include_body, redact_headers
) VALUES (
    $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
    100, true, false, '{}'::text[]
)
ON CONFLICT (id) DO NOTHING
`, ruleID, accountID, appID, sourceDeploymentID, mirrorDeploymentID); err != nil {
		t.Fatalf("seed mirror rule: %v", err)
	}
	return accountID, sourceDeploymentID, mirrorDeploymentID, ruleID
}

// seedMirrorLedgerRow inserts one mirror_invocation_results row
// for (ruleID, completedAt) with the supplied boolean flags. Used
// by the rollup + sweep tests to populate the ledger without going
// through the gateway goroutine.
//
// The shape matches the schema PR-A1 shipped (migration 00386):
// all four boolean diff flags + crashed are real columns. The
// NOT NULL FK columns (account_id, source_deployment_id,
// mirror_deployment_id, request_id) come from the seeded fixture
// the test creates via seedMirrorFixture. Returns the new row ID.
func seedMirrorLedgerRow(t *testing.T, pool *pgxpool.Pool, ruleID, accountID, sourceDeploymentID, mirrorDeploymentID string, completedAt time.Time, statusDiff, schemaDiff, bodyDiff, crashed bool) string {
	t.Helper()
	id := uuid.NewString()
	requestID := uuid.NewString()
	_, err := pool.Exec(context.Background(), `
INSERT INTO mirror_invocation_results (
    id, mirror_rule_id, account_id, app_id,
    source_deployment_id, mirror_deployment_id,
    status_diff, schema_diff, body_diff, crashed,
    request_id, completed_at
) VALUES ($1, $2::uuid, $3::uuid, (SELECT app_id FROM mirror_rules WHERE id = $2::uuid),
          $4::uuid, $5::uuid,
          $6, $7, $8, $9,
          $10, $11)
`, id, ruleID, accountID, sourceDeploymentID, mirrorDeploymentID, statusDiff, schemaDiff, bodyDiff, crashed, requestID, completedAt)
	if err != nil {
		t.Fatalf("seed mirror ledger row: %v", err)
	}
	return id
}

// TestE2E_MirrorDispatch_HappyPath pins the load-bearing general
// path from public rule creation through customer traffic and
// schedd mirror admission. It deliberately keeps the source
// deployment at 100% traffic and the mirror deployment at 0%:
// the mirror is a shadow target, never a customer route.
//
// The fake VMMD removes KVM/Firecracker from this test while the
// real apid, schedd, gatewayd, Postgres, pg_notify, and gRPC
// boundaries remain in the path. The mirror HTTP forwarding
// contract is covered separately by gateway unit tests; this test
// owns the previously missing cross-daemon admission contract.
func TestE2E_MirrorDispatch_HappyPath(t *testing.T) {
	artifactRoot := t.TempDir()
	f := newNormalPathFixtureWithPlanAndEnv(t, "normal-mirror-dispatch", api.PlanPro,
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT="+artifactRoot,
		"FAAS_APPS_ROOT="+artifactRoot,
		"FAAS_STORAGE_CACHE_DIR=",
	)
	if f == nil {
		return
	}
	artifacts, err := artifactstorage.NewLocalStorageBackend(artifactRoot)
	if err != nil {
		t.Fatalf("create mirror artifact backend: %v", err)
	}
	signer, err := cosign.NewLocalSigner(f.h.SignKeyPath, artifacts, nil)
	if err != nil {
		t.Fatalf("create mirror artifact signer: %v", err)
	}
	sourceDeployment, sourceInstance := createNormalPathExplicitTrafficDeployment(
		t, f, "mirror-source", 100)
	mirrorDeployment, _ := createNormalPathExplicitTrafficDeployment(
		t, f, "mirror-target", 0)
	mirrorLayerKey := "apps/" + f.app.Slug + "/" + mirrorDeployment.ID + ".ext4"
	mirrorLayer := []byte("gregale mirror dispatch fixture artifact\n")
	if err := artifacts.Put(f.ctx, mirrorLayerKey, strings.NewReader(string(mirrorLayer))); err != nil {
		t.Fatalf("publish mirror layer: %v", err)
	}
	if err := signer.Sign(f.ctx, mirrorLayerKey, cosign.SigKeyFor(mirrorLayerKey)); err != nil {
		t.Fatalf("sign mirror layer: %v", err)
	}
	if err := f.store.SetDeploymentRootfs(f.ctx, mirrorDeployment.ID,
		"/e2e/"+mirrorLayerKey, mirrorLayerKey, int64(len(mirrorLayer))); err != nil {
		t.Fatalf("publish mirror rootfs metadata: %v", err)
	}
	f.vmmd.SetVersion(sourceInstance.ID, "mirror-source")

	// Publish the live deployment/instance state to the real gateway
	// picker. The API-created rule below supplies the separate
	// kind="mirror" notification that refreshes the mirror-rule cache.
	notifyNormalPathDeploymentChanged(t, f, sourceDeployment.ID)
	notifyNormalPathDeploymentChanged(t, f, mirrorDeployment.ID)
	notifyNormalPathInstanceChanged(t, f, sourceInstance.ID, string(state.StateRunning))

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/"+f.app.Slug+"/mirrors", api.CreateMirrorRuleRequest{
			SourceDeploymentID: sourceDeployment.ID,
			MirrorDeploymentID: mirrorDeployment.ID,
			Percent:            100,
		})
	if statusCode != http.StatusCreated {
		t.Fatalf("create mirror rule: status=%d body=%s", statusCode, body)
	}
	var rule api.MirrorRuleResponse
	if err := json.Unmarshal(body, &rule); err != nil {
		t.Fatalf("decode mirror rule: %v body=%s", err, body)
	}
	if rule.SourceDeploymentID != sourceDeployment.ID || rule.MirrorDeploymentID != mirrorDeployment.ID {
		t.Fatalf("mirror rule deployments=(%s,%s), want=(%s,%s)",
			rule.SourceDeploymentID, rule.MirrorDeploymentID,
			sourceDeployment.ID, mirrorDeployment.ID)
	}

	if got := waitForNormalPathTrafficResponse(t, f, "normal-path:mirror-source\n", 10*time.Second); string(got) != "normal-path:mirror-source\n" {
		t.Fatalf("source response=%q, want source deployment response", got)
	}

	mirrorInstance := waitForMirrorInstance(t, f.store, f.app.ID, mirrorDeployment.ID, 10*time.Second)
	if mirrorInstance.Mode != string(state.InstanceModeMirror) {
		t.Fatalf("mirror instance mode=%q, want %q", mirrorInstance.Mode, state.InstanceModeMirror)
	}
	if mirrorInstance.State != string(state.StateRunning) {
		t.Fatalf("mirror instance state=%q, want %q", mirrorInstance.State, state.StateRunning)
	}
}

func waitForMirrorInstance(t *testing.T, store *state.PgStore, appID, deploymentID string, timeout time.Duration) state.Instance {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []state.Instance
	for time.Now().Before(deadline) {
		instances, err := store.ListInstancesForApp(context.Background(), appID)
		if err != nil {
			t.Fatalf("list mirror instances: %v", err)
		}
		last = instances
		for _, instance := range instances {
			if instance.DeploymentID == deploymentID && instance.Mode == string(state.InstanceModeMirror) {
				return instance
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("mirror instance for deployment %s not admitted within %s; instances=%+v", deploymentID, timeout, last)
	return state.Instance{}
}

// TestE2E_MirrorRollup_AggregatesByRuleHour pins the rollup
// contract end-to-end: 5 ledger rows in the trailing hour
// collapse into one mirror_invocation_summary row with
// total_invocations = 5 (the additive-merge behaviour).
//
// Runs against the real Postgres ledger the dispatch goroutine
// writes to in production, so a schema drift between the
// gateway's INSERT and the rollup's SELECT surfaces here, not
// at 3am in a customer dashboard.
func TestE2E_MirrorRollup_AggregatesByRuleHour(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	appID := uuid.NewString()
	accountID, sourceDeploymentID, mirrorDeploymentID, ruleID := seedMirrorFixture(t, pool, appID)
	now := time.Now().UTC()

	// Anchor all rows at the current hour boundary so the fixture cannot
	// straddle two hour buckets when the test runs near an hour boundary.
	// The dispatch goroutine uses completed_at for the rollup window.
	completedAt := now.Truncate(time.Hour)
	for i := 0; i < 5; i++ {
		seedMirrorLedgerRow(t, pool, ruleID, accountID, sourceDeploymentID, mirrorDeploymentID,
			completedAt,
			false, false, false, false)
	}

	// Roll the trailing hour. RollupOnce runs the additive-merge
	// UPSERT keyed on (rule_id, hour_bucket).
	start := now.Add(-1 * time.Hour)
	end := now.Add(1 * time.Hour)
	if _, err := mirrorRollup.RollupOnce(ctx, mirrorPoolAdapter{pool}, start, end); err != nil {
		t.Fatalf("RollupOnce: %v", err)
	}

	// Assert that the rows collapsed into one summary bucket with
	// total_invocations = 5.
	var buckets, total int64
	err := pool.QueryRow(ctx, `
SELECT count(*), COALESCE(sum(total_invocations), 0)
FROM mirror_invocation_summary
WHERE rule_id = $1
`, ruleID).Scan(&buckets, &total)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if buckets != 1 {
		t.Errorf("summary buckets = %d, want 1", buckets)
	}
	if total != 5 {
		t.Errorf("total_invocations = %d, want 5", total)
	}
}

// TestE2E_MirrorSweep_DeletesOnlyStaleRows pins the retention
// contract: 3 rows aged 8d + 2 rows aged 1d → sweep with a 7d
// cutoff deletes the 3 stale rows and leaves the 2 fresh rows.
//
// This is the customer-facing promise of the ADR-124 §14
// acceptance gate: "after 7d, the customer's mirror_invocation_results
// table only has rows for the trailing week, and the per-hour
// summary preserves the totals".
func TestE2E_MirrorSweep_DeletesOnlyStaleRows(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	appID := uuid.NewString()
	accountID, sourceDeploymentID, mirrorDeploymentID, ruleID := seedMirrorFixture(t, pool, appID)
	now := time.Now().UTC()

	// 3 stale (8d), 2 fresh (1d).
	for i := 0; i < 3; i++ {
		seedMirrorLedgerRow(t, pool, ruleID, accountID, sourceDeploymentID, mirrorDeploymentID,
			now.Add(-(8*24*time.Hour + time.Duration(i)*time.Hour)),
			false, false, false, false)
	}
	for i := 0; i < 2; i++ {
		seedMirrorLedgerRow(t, pool, ruleID, accountID, sourceDeploymentID, mirrorDeploymentID,
			now.Add(-(24*time.Hour + time.Duration(i)*time.Hour)),
			false, false, false, false)
	}

	cutoff := now.Add(-7 * 24 * time.Hour)
	if _, err := mirrorRollup.SweepOldLedgerRows(ctx, mirrorPoolAdapter{pool}, cutoff); err != nil {
		t.Fatalf("SweepOldLedgerRows: %v", err)
	}

	var remaining int64
	err := pool.QueryRow(ctx, `
SELECT count(*)
FROM mirror_invocation_results
WHERE mirror_rule_id = $1
`, ruleID).Scan(&remaining)
	if err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if remaining != 2 {
		t.Errorf("remaining ledger rows = %d, want 2 (sweep deleted %d stale)", remaining, 5-remaining)
	}
}

// mirrorPoolAdapter (PR-A3 commit 5) adapts *pgxpool.Pool to the
// mirror.execer contract (Exec returning (int64, error)). Mirrors
// cmd/schedd/main.go::mirrorPoolAdapter + cmd/meterd/main.go::poolAdapter
// — both wrappers exist because pkg/mirror / pkg/meter deliberately
// avoid importing pgxpool directly so the rollup package stays
// unit-testable without a Postgres dependency. Tests in cmd/e2e
// reach the rollup via the same seam.
type mirrorPoolAdapter struct{ pool *pgxpool.Pool }

func (a mirrorPoolAdapter) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := a.pool.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}
