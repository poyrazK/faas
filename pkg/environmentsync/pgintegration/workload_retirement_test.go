package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func retireEmptyEnvironment(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitOpsLease, state.EnvironmentWorkloadRetirement) {
	t.Helper()
	store := basic.(intentTestStore)
	source, prior, app := intentFixture(t, store, "enforce")
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimEnvironmentGitOps(t.Context(), "retirement-initial", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), old, claimedIntentPlan(t, store, old, prior)); err != nil {
		t.Fatal(err)
	}
	prune := true
	source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
	if err != nil {
		t.Fatal(err)
	}
	definition := prior.Definition
	definition.Workloads = map[string]api.EnvironmentWorkload{}
	definition.WorkloadPruningPolicy = "retain"
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40))); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "retirement-review", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	if !plan.CanApply() || !plan.HasDrift() {
		t.Fatalf("retirement plan: %+v", plan)
	}
	if len(plan.RetirementImpacts) != 1 || plan.RetirementImpacts[0].AppID != app.ID {
		t.Fatalf("missing scoped impact: %+v", plan.RetirementImpacts)
	}
	retirement := basic.(state.EnvironmentGitOpsWorkloadRetirementStore)
	if _, err := retirement.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, environmentsync.Plan{Hash: strings.Repeat("0", 64)}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted stale retirement review: %v", err)
	}
	steps, err := retirement.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, plan)
	if err != nil || len(steps) != 1 {
		t.Fatalf("reserve: %+v %v", steps, err)
	}
	admission := basic.(state.EnvironmentWorkloadRetirementAdmissionStore)
	if blocked, err := admission.EnvironmentWorkloadAdmissionRetired(t.Context(), app.ID, "production"); err != nil || blocked {
		t.Fatalf("reservation withdrew execution before cutover: %t %v", blocked, err)
	}
	fresh := claimedIntentPlan(t, store, lease, desired)
	if !fresh.CanApply() || fresh.HasDrift() || fresh.Hash == plan.Hash {
		t.Fatalf("reservation identity not reflected in plan: %+v", fresh)
	}
	if ready, err := retirement.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, fresh); err != nil || !ready {
		t.Fatalf("empty cutover: %t %v", ready, err)
	}
	for _, scope := range []string{"production", "default", ""} {
		if blocked, err := admission.EnvironmentWorkloadAdmissionRetired(t.Context(), app.ID, scope); err != nil || !blocked {
			t.Fatalf("cutover reopened admission via scope %q: %t %v", scope, blocked, err)
		}
	}
	if blocked, err := admission.EnvironmentWorkloadAdmissionRetired(t.Context(), app.ID, "staging"); err != nil || blocked {
		t.Fatalf("neighbor environment retired: %t %v", blocked, err)
	}
	if ready, err := retirement.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || !ready {
		t.Fatalf("no-execution drain: %t %v", ready, err)
	}
	records, err := retirement.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), source.AccountID, source.ID)
	if err != nil || len(records) != 1 || records[0].Phase != "retired" {
		t.Fatalf("retained journal: %+v %v", records, err)
	}
	assertVariable(t, store, source, app, "production", "MODE", "production")
	assertVariable(t, store, source, app, "production", "MANUAL", "keep")
	assertVariable(t, store, source, app, "staging", "MODE", "staging")
	if _, err := store.AppByID(t.Context(), app.ID); err != nil {
		t.Fatal("retirement deleted shared app", err)
	}
	if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Mode: "report"}); err == nil {
		t.Fatal("source transfer bypassed retained recovery identity")
	}
	return lease, records[0]
}
func beginRetainedRecovery(t *testing.T, basic gitOpsTestStore, previous state.EnvironmentGitOpsLease, r state.EnvironmentWorkloadRetirement) state.EnvironmentGitOpsLease {
	t.Helper()
	intent := basic.(state.EnvironmentGitOpsIntentStore)
	source := previous.Source
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", WorkloadRecoveries: map[string]string{"api": r.AppID}, Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "example/api@sha256:" + strings.Repeat("c", 64), FullRootfs: true}}}}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := basic.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40))); err != nil {
		t.Fatal(err)
	}
	lease, err := basic.ClaimEnvironmentGitOps(t.Context(), "retained-recovery", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := intent.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !adoption.CanApply() {
		t.Fatalf("recovery field adoption: %+v %v", adoption, err)
	}
	if err := intent.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	plan := planForStore(t, intent, lease, desired)
	if !plan.CanApply() {
		t.Fatalf("reviewed recovery blocked: %+v", plan)
	}
	store := basic.(state.EnvironmentGitOpsWorkloadRetirementStore)
	steps, err := store.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, plan)
	if err != nil || len(steps) != 1 {
		t.Fatalf("recovery reservation: %+v %v", steps, err)
	}
	records, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), source.AccountID, source.ID)
	if err != nil || len(records) != 1 || records[0].ID != r.ID || records[0].Phase != "recovering" || records[0].RecoveryRevisionID != lease.Revision.ID {
		t.Fatalf("original recovery identity: %+v %v", records, err)
	}
	if blocked, err := basic.(state.EnvironmentWorkloadRetirementAdmissionStore).EnvironmentWorkloadAdmissionRetired(t.Context(), r.AppID, "production"); err != nil || !blocked {
		t.Fatalf("recovery opened without qualification: %t %v", blocked, err)
	}
	if _, err := store.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("nonempty recovery used empty cutover: %v", err)
	}
	return lease
}
func TestEnvironmentGitOpsRetainedWorkloadRemovalAndRecovery(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, r := retireEmptyEnvironment(t, basic)
		beginRetainedRecovery(t, basic, lease, r)
	})
}
func TestPgEnvironmentGitOpsRetirementHistorySurvivesCleanupAttempts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, original := retireEmptyEnvironment(t, store)
	for _, attempt := range []struct{ name, query, id string }{
		{"journal", "DELETE FROM environment_workload_retirements WHERE id=$1", original.ID},
		{"source cascade", "DELETE FROM environment_git_sources WHERE id=$1", lease.Source.ID},
	} {
		var rejected *pgconn.PgError
		if _, err := pool.Exec(t.Context(), attempt.query, attempt.id); !errors.As(err, &rejected) || rejected.Code != "23514" {
			t.Fatalf("%s cleanup did not hit its retention guard: %v", attempt.name, err)
		}
		rows, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), lease.Source.AccountID, lease.Source.ID)
		if err != nil || len(rows) != 1 || rows[0].ID != original.ID || rows[0].Phase != "retired" {
			t.Fatalf("%s cleanup changed retained evidence: %+v %v", attempt.name, rows, err)
		}
		if blocked, err := store.EnvironmentWorkloadAdmissionRetired(t.Context(), original.AppID, lease.Source.EnvironmentSlug); err != nil || !blocked {
			t.Fatalf("%s cleanup reopened admission: %t %v", attempt.name, blocked, err)
		}
	}
}

func TestPgEnvironmentGitOpsRetirementReplayPreservesRetainedRecovery(t *testing.T) {
	for _, phase := range []string{"retired", "recovering"} {
		t.Run(phase, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			lease, original := retireEmptyEnvironment(t, store)
			if phase == "recovering" {
				lease = beginRetainedRecovery(t, store, lease, original)
			}
			before, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), lease.Source.AccountID, lease.Source.ID)
			if err != nil || len(before) != 1 || before[0].Phase != phase {
				t.Fatalf("retained fixture: %+v %v", before, err)
			}
			// A later migration may refine a guard. Replaying this earlier Up
			// must retain that definition as well as the populated journal.
			if _, err := pool.Exec(t.Context(), `CREATE OR REPLACE FUNCTION environment_workload_retirement_gateway_roster() RETURNS text[] LANGUAGE sql STABLE AS $$
 -- Later guard refinement retained across ledger replay.
 SELECT ARRAY(SELECT name FROM compute_nodes WHERE active AND role IN ('compute-only','compute-node')
  AND nullif(btrim(gateway_target_url),'') IS NOT NULL ORDER BY name)
$$`); err != nil {
				t.Fatal(err)
			}
			const guardQuery = `SELECT pg_get_functiondef('environment_workload_retirement_gateway_roster()'::regprocedure)`
			var beforeGuard string
			if err := pool.QueryRow(t.Context(), guardQuery).Scan(&beforeGuard); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if tag, err := pool.Exec(t.Context(), `DELETE FROM goose_db_version WHERE version_id=20261015094302001`); err != nil || tag.RowsAffected() != 1 {
					t.Fatalf("remove exact retirement ledger entry: %v %v", tag, err)
				}
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal("replay retirement migration", err)
				}
				after, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), lease.Source.AccountID, lease.Source.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("replay changed retained recovery evidence: %+v %v", after, err)
				}
				var afterGuard string
				if err := pool.QueryRow(t.Context(), guardQuery).Scan(&afterGuard); err != nil || beforeGuard != afterGuard {
					t.Fatalf("replay replaced a later guard refinement: %v", err)
				}
				if blocked, err := store.EnvironmentWorkloadAdmissionRetired(t.Context(), original.AppID, "production"); err != nil || !blocked {
					t.Fatalf("replay reopened admission: %t %v", blocked, err)
				}
				var rejected *pgconn.PgError
				if _, err := pool.Exec(t.Context(), `DELETE FROM environment_workload_retirements WHERE id=$1`, original.ID); !errors.As(err, &rejected) || rejected.Code != "23514" {
					t.Fatalf("replay removed the retention guard: %v", err)
				}
			}
			app, err := store.AppByID(t.Context(), original.AppID)
			if err != nil {
				t.Fatal("replay lost the retained app", err)
			}
			assertVariable(t, store, lease.Source, app, "production", "MODE", "production")
			assertVariable(t, store, lease.Source, app, "staging", "MODE", "staging")
			if phase == "retired" {
				beginRetainedRecovery(t, store, lease, original)
			}
		})
	}
}

func TestPgEnvironmentGitOpsRetirementCannotForgeRecoveredEvidence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, r := retireEmptyEnvironment(t, store)
	lease = beginRetainedRecovery(t, store, lease, r)
	if _, err := pool.Exec(t.Context(), `UPDATE environment_workload_retirements SET phase='recovered',recovered_at=clock_timestamp() WHERE id=$1`, r.ID); err == nil {
		t.Fatal("journal advanced without its lease")
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), `SELECT set_config('gregale.gitops_lease',$1,true),set_config('gregale.gitops_activation',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE environment_workload_retirements SET phase='recovered',recovered_at=clock_timestamp() WHERE id=$1`, r.ID); err == nil {
		t.Fatal("historical retirement reopened without fresh qualified activation")
	}
}

// Uses the existing qualification-receipt fixture; no native VM execution is
// claimed here. It proves the SQL cutover, withdrawal and fresh recovery gates.
func TestPgEnvironmentGitOpsActivatedWorkloadsRetireAndRequireFreshRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, requests := preparedRequestModeQualificationFixture(t, store)
	var original api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &original); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(original)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	for _, request := range requests {
		completeRequestQualificationReceipts(t, store, request)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
		t.Fatalf("initial activation: %t %v", active, err)
	}
	gateways := []string{"edge-a", "edge-b"}
	for _, name := range gateways {
		registerServingGateway(t, store, name)
	}
	servingReceipt, _, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, gateways)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range servingReceipt.Routes {
		for _, node := range gateways {
			if _, _, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, servingReceipt.GraphID, route.Generation, node); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The serving protocol already withdrew these fixture-only inherited
	// baselines. They have no executions; finish their scheduler-owned drain.
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status='superseded' WHERE app_id IN (SELECT app_id FROM environment_gitops_resources WHERE source_id=$1) AND environment_workload_runtime IS NULL`, lease.Source.ID); err != nil {
		t.Fatal(err)
	}
	prune := true
	source, err := store.UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Prune: &prune})
	if err != nil {
		t.Fatal(err)
	}
	empty := original
	empty.Workloads = map[string]api.EnvironmentWorkload{}
	empty.WorkloadPruningPolicy = "retain"
	retiredDesired, err := environmentsync.Compile(empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, retiredDesired, strings.Repeat("d", 40))); err != nil {
		t.Fatal(err)
	}
	retirementLease, err := store.ClaimEnvironmentGitOps(t.Context(), "activated-retirement", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reviewed := claimedIntentPlan(t, store, retirementLease, retiredDesired)
	if !reviewed.CanApply() {
		t.Fatalf("review: %+v", reviewed)
	}
	if _, err := store.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), retirementLease, reviewed); err != nil {
		t.Fatal(err)
	}
	fresh := claimedIntentPlan(t, store, retirementLease, retiredDesired)
	if cutover, err := store.ActivateEnvironmentGitOpsEmptyRetirement(t.Context(), retirementLease, fresh); err != nil || !cutover {
		t.Fatalf("retirement cutover: %t %v", cutover, err)
	}
	pending, err := store.PendingEnvironmentGitOpsWorkloadRetirements(t.Context(), retirementLease, []string{"edge-a", "edge-b"})
	if err != nil || len(pending) != len(requests) {
		t.Fatalf("withdrawal journal: %+v %v", pending, err)
	}
	for _, r := range pending {
		if !slices.Contains(r.DeploymentIDs, r.RouteDeploymentID) || r.RouteGeneration <= 0 {
			t.Fatalf("original withdrawal identity: %+v", r)
		}
		if err := store.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), retirementLease, r.ID, r.RouteGeneration, "edge-a"); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := store.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), retirementLease); err != nil || ready {
		t.Fatalf("partial roster retired executions: %t %v", ready, err)
	}
	for _, r := range pending {
		for _, id := range r.DeploymentIDs {
			d, err := store.DeploymentByID(t.Context(), id)
			if err != nil || d.Status != state.DeployLive {
				t.Fatalf("drain before complete roster: %+v %v", d, err)
			}
		}
	}
	for _, r := range pending {
		if err := store.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), retirementLease, r.ID, r.RouteGeneration, "edge-b"); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := store.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), retirementLease); err != nil || !ready {
		t.Fatalf("withdrawn original execution: %t %v", ready, err)
	}
	records, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), source.AccountID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovery := original
	recovery.WorkloadRecoveries = map[string]string{}
	ids := map[string]string{}
	for _, r := range records {
		if r.Phase != "retired" {
			t.Fatalf("not retired: %+v", r)
		}
		recovery.WorkloadRecoveries[strings.TrimPrefix(r.Resource, "workload/")] = r.AppID
		ids[r.Resource] = r.ID
	}
	recoveryDesired, err := environmentsync.Compile(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(retirementLease.Source, recoveryDesired, strings.Repeat("e", 40))); err != nil {
		t.Fatal(err)
	}
	recoveryLease, err := store.ClaimEnvironmentGitOps(t.Context(), "activated-recovery", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recoveryPlan := claimedIntentPlan(t, store, recoveryLease, recoveryDesired)
	if !recoveryPlan.CanApply() {
		t.Fatalf("reviewed recovery: %+v", recoveryPlan)
	}
	if _, err := store.BeginEnvironmentGitOpsWorkloadRetirements(t.Context(), recoveryLease, recoveryPlan); err != nil {
		t.Fatal(err)
	}
	recoveryPlan = claimedIntentPlan(t, store, recoveryLease, recoveryDesired)
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), recoveryLease, recoveryPlan); err != nil {
		t.Fatal(err)
	}
	recoveryPlan = claimedIntentPlan(t, store, recoveryLease, recoveryDesired)
	recoveryCandidates, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), recoveryLease, recoveryPlan)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range recoveryCandidates {
		if err := store.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed-recovery.ext4", "recovered-"+candidate.Resource, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), recoveryLease, recoveryPlan); err != nil {
		t.Fatal(err)
	}
	recoveryRequests, err := store.QueueEnvironmentGitOpsQualification(t.Context(), recoveryLease, recoveryPlan)
	if err != nil || len(recoveryRequests) != len(requests) {
		t.Fatalf("fresh requests: %+v %v", recoveryRequests, err)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), recoveryLease, recoveryPlan); err != nil || active {
		t.Fatalf("historical qualification reopened recovery: %t %v", active, err)
	}
	for _, request := range recoveryRequests {
		completeRequestQualificationReceipts(t, store, request)
	}
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), recoveryLease, recoveryPlan); err != nil || !active {
		t.Fatalf("fresh recovery activation: %t %v", active, err)
	}
	recovered, err := store.ListEnvironmentGitOpsWorkloadRetirements(t.Context(), source.AccountID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recovered {
		if r.ID != ids[r.Resource] || r.Phase != "recovered" || r.RecoveryGeneration != recoveryLease.Source.Generation {
			t.Fatalf("recovery identity: %+v", r)
		}
		if blocked, err := store.EnvironmentWorkloadAdmissionRetired(t.Context(), r.AppID, "production"); err != nil || blocked {
			t.Fatalf("freshly qualified recovery remained closed: %t %v", blocked, err)
		}
	}
}
