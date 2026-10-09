// adr: 521
package acceptance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func expireOperationCodeTimestamps(t *testing.T, pool *pgxpool.Pool, op state.Operation) {
	t.Helper()
	ctx := t.Context()
	past := time.Now().Add(-time.Hour).UTC()
	if _, err := pool.Exec(ctx, `UPDATE customer_operations SET expires_at=$2,
record=jsonb_set(record,'{expires_at}',to_jsonb($2::timestamptz)) WHERE id=$1`, op.ID, past); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployment_revision_pins SET expires_at=$2 WHERE deployment_id=$1`, op.DeploymentID, past); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE customer_operation_code_pins SET expires_at=$2 WHERE deployment_id=$1`, op.DeploymentID, past); err != nil {
		t.Fatal(err)
	}
}

func requireRetainedOperationCode(t *testing.T, s *state.PgStore, op state.Operation) {
	t.Helper()
	ctx := t.Context()
	dep, err := s.DeploymentByID(ctx, op.DeploymentID)
	if err != nil || dep.Status != state.DeployLive || dep.TrafficPercent != 0 {
		t.Fatalf("active code retired: %+v %v", dep, err)
	}
	if _, err := s.ResolveRevisionPin(ctx, op.AppID, op.Scope, op.DeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired public revision pin gained private access: %v", err)
	}
	inv, err := s.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	_, selected, err := state.ResolveInvocationVersion(ctx, s, inv)
	if err != nil || selected.DeploymentID != op.DeploymentID || selected.ReleaseID != op.ReleaseID {
		t.Fatalf("operation lost pinned version: %+v %v", selected, err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 0 {
		t.Fatalf("active operation code swept: %d %v", count, err)
	}
}

func TestPgOperationCodeRetentionPastTimestamp(t *testing.T) {
	for _, active := range []api.OperationState{api.OperationAccepted, api.OperationRunning} {
		t.Run(string(active), func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			s := state.NewPgStore(pool)
			ctx, acct, app, def, alice, _ := operationFixture(t, s)
			manifest := app.Manifest
			manifest.RevisionPinTTLSeconds = 3600
			if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
				PlatformTenantID: alice.ID, IdempotencyKey: "waiting-past-code-window", Input: []byte(`{"count":1}`)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE customer_operations SET state=$2,record=jsonb_set(record,'{state}',to_jsonb($2::text)) WHERE id=$1`, op.ID, string(active)); err != nil {
				t.Fatal(err)
			}
			expireOperationCodeTimestamps(t, pool, op)
			next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:replacement"})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
				t.Fatal(err)
			}
			// Cutover creates its own public TTL now that admission no longer
			// writes that table. Expire that legitimate grant independently.
			expireOperationCodeTimestamps(t, pool, op)
			requireRetainedOperationCode(t, s, op)
			settled, err := s.CancelOperation(ctx, acct.ID, alice.ID, op.ID, op.Generation)
			if err != nil || !settled.ExpiresAt.After(time.Now()) {
				t.Fatalf("settlement did not refresh retention: %+v %v", settled, err)
			}
			if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 0 {
				t.Fatalf("settled result/recovery window lost code: %d %v", count, err)
			}
			expireOperationCodeTimestamps(t, pool, settled)
			if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 1 {
				t.Fatalf("expired settled work did not release code: %d %v", count, err)
			}
		})
	}
}

func createOperationGraphApp(t *testing.T, ctx context.Context, s *state.PgStore, acct, project, name string) (state.App, state.Deployment) {
	t.Helper()
	app, err := s.CreateApp(ctx, state.App{AccountID: acct, ProjectID: project, Slug: name, WorkloadName: name,
		Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, Scope: "production", ImageDigest: "sha256:" + name})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return app, dep
}

func TestPgOperationRetainsWholeReleaseAcrossLongWait(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "operation-graph@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "operation-graph"})
	if err != nil {
		t.Fatal(err)
	}
	origin, originDep := createOperationGraphApp(t, ctx, s, acct.ID, project.ID, "origin")
	target, targetDep := createOperationGraphApp(t, ctx, s, acct.ID, project.ID, "target")
	first, err := s.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{
		{AppID: origin.ID, DeploymentID: originDep.ID}, {AppID: target.ID, DeploymentID: targetDep.ID}})
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: origin.ID, Scope: "production", DeploymentID: originDep.ID, ReleaseID: first.ID, Spec: operationSpec()}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, ReleaseID: first.ID, IdempotencyKey: "long-graph-wait", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	nextMembers := make([]state.ProjectReleaseMember, 0, 2)
	for _, app := range []state.App{origin, target} {
		next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, Scope: "production", ImageDigest: "sha256:next-" + app.Slug})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		nextMembers = append(nextMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: next.ID})
	}
	if _, err := s.PublishProjectReleaseSet(ctx, acct.ID, project.ID, "production", 1800, nextMembers); err != nil {
		t.Fatal(err)
	}
	expireOperationCodeTimestamps(t, pool, op)
	if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at=now()-interval '1 hour' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployment_revision_pins SET expires_at=now()-interval '1 hour' WHERE deployment_id=$1`, targetDep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE customer_operation_code_pins SET expires_at=now()-interval '1 hour' WHERE deployment_id=$1`, targetDep.ID); err != nil {
		t.Fatal(err)
	}
	requireRetainedOperationCode(t, s, op)
	if _, _, err := s.ResolveProjectRelease(ctx, target.ID, "production", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("private retention extended an expired public release selector: %v", err)
	}
	retry := state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "long-graph-wait", Input: []byte(`{"count":1}`)}
	if repeated, fresh, err := s.AdmitOperation(ctx, retry); err != nil || fresh || repeated.ID != op.ID {
		t.Fatalf("public expiry lost an existing operation receipt: %+v %v %v", repeated, fresh, err)
	}
	retry.IdempotencyKey = "fresh-expired-private-graph"
	if _, _, err := s.AdmitOperation(ctx, retry); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("private code retention granted a fresh submission: %v", err)
	}
	if release, dep, err := s.ResolveServiceRelease(ctx, origin.ID, originDep.ID, target.ID, first.ID); err != nil || release != first.ID || dep != targetDep.ID {
		t.Fatalf("waiting operation lost service graph: %s/%s %v", release, dep, err)
	}
	if _, _, err := s.ResolveProjectRelease(ctx, target.ID, "default", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("retained graph escaped environment: %v", err)
	}
	if _, _, err := s.ResolveServiceRelease(ctx, origin.ID, nextMembers[0].DeploymentID, target.ID, first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("new source acquired old graph membership: %v", err)
	}
	assertGraphAdmissionRechecksAfterTargetLock(t, pool, s, op, def, tenant, targetDep)
	// An operation's JSON release pointer alone cannot retain another graph.
	if _, err := pool.Exec(ctx, `UPDATE customer_operations SET record=jsonb_set(record,'{deployment_id}',to_jsonb($2::text)) WHERE id=$1`, op.ID, nextMembers[0].DeploymentID); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 2 {
		t.Fatalf("mismatched admission metadata retained graph: %d %v", count, err)
	}
	if _, _, err := s.ResolveProjectRelease(ctx, target.ID, "production", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unowned graph remained usable: %v", err)
	}
	if _, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "implicit-expired-graph", Input: []byte(`{"count":1}`)}); err == nil {
		t.Fatal("definition's implicit expired release was resurrected by admission")
	}
}

func admitPgOperationCode(t *testing.T, s *state.PgStore, app state.App, dep state.Deployment, tenant state.PlatformTenant) state.Operation {
	t.Helper()
	def, err := s.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: app.AccountID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: operationSpec()}})
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: app.AccountID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: dep.ID, Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestPgOperationCodeSurvivesServiceHandoffs(t *testing.T) {
	for _, action := range []string{"promote", "abort"} {
		t.Run(action, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			s := state.NewPgStore(pool)
			ctx, _, app, def, tenant, _ := operationFixture(t, s)
			manifest := app.Manifest
			manifest.ExecutionMode = api.ExecutionModeService
			manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}
			if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
				t.Fatal(err)
			}
			stable, err := s.DeploymentByID(ctx, def.DeploymentID)
			if err != nil {
				t.Fatal(err)
			}
			stableOp := admitPgOperationCode(t, s, app, stable, tenant)
			now := time.Now().UTC()
			next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:service-next", RolloutState: "rolling_out", RolloutStartedAt: &now})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
				t.Fatal(err)
			}
			nextOp := admitPgOperationCode(t, s, app, next, tenant)
			expireOperationCodeTimestamps(t, pool, stableOp)
			expireOperationCodeTimestamps(t, pool, nextOp)
			if action == "promote" {
				if _, err := s.FinalizeServiceRollout(ctx, next.ID); err != nil {
					t.Fatal(err)
				}
				requireRetainedOperationCode(t, s, stableOp)
			} else {
				aborted, err := s.AbortServiceRollout(ctx, next.ID, "readiness failed")
				if err != nil || aborted.Status != state.DeployLive || aborted.RolloutState != "aborted" || aborted.RolloutCompletedAt != nil || aborted.RolloutAbortedAt == nil {
					t.Fatalf("retained aborted target: %+v %v", aborted, err)
				}
				requireRetainedOperationCode(t, s, nextOp)
				settled, err := s.CancelOperation(ctx, app.AccountID, tenant.ID, nextOp.ID, nextOp.Generation)
				if err != nil {
					t.Fatal(err)
				}
				expireOperationCodeTimestamps(t, pool, settled)
				if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 1 {
					t.Fatalf("aborted target retained after operation expired: %d %v", count, err)
				}
			}
		})
	}
}

func TestPgOperationCodeSurvivesAutoRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, _, app, def, tenant, _ := operationFixture(t, s)
	old, err := s.DeploymentByID(ctx, def.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	oldOp := admitPgOperationCode(t, s, app, old, tenant)
	next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	nextOp := admitPgOperationCode(t, s, app, next, tenant)
	expireOperationCodeTimestamps(t, pool, oldOp)
	expireOperationCodeTimestamps(t, pool, nextOp)
	if target, err := s.LatestSupersededDeployment(ctx, app.ID); err != nil || target.ID != old.ID {
		t.Fatalf("retained old revision cannot be selected: %+v %v", target, err)
	}
	if id, err := s.AutoRollbackDeploymentsTx(ctx, app.ID, next.ID); err != nil || id != old.ID {
		t.Fatalf("retained old revision cannot be restored: %s %v", id, err)
	}
	requireRetainedOperationCode(t, s, nextOp)
	restored, err := s.DeploymentByID(ctx, old.ID)
	if err != nil || restored.Status != state.DeployLive || restored.TrafficPercent != 100 || restored.RolloutState != "complete" {
		t.Fatalf("rollback target did not become the weighted route: %+v %v", restored, err)
	}
}

func TestPgOperationCodeCleanupRechecksReferencesAfterLock(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "gc-lock-order", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	settled, err := s.CancelOperation(ctx, acct.ID, tenant.ID, op.ID, op.Generation)
	if err != nil {
		t.Fatal(err)
	}
	expireOperationCodeTimestamps(t, pool, settled)
	// Model a newly committed active reference under admission's app lock.
	// The expired pin deliberately stays unchanged to distinguish a fresh
	// operation-reference snapshot from a target-row expiry recheck.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM apps WHERE id=$1 FOR SHARE`, app.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_operations SET state='accepted',record=jsonb_set(record,'{state}','"accepted"'::jsonb) WHERE id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	type cleanupResult struct {
		count int64
		err   error
	}
	finished := make(chan cleanupResult, 1)
	go func() { count, err := s.ExpireRevisionPins(ctx); finished <- cleanupResult{count, err} }()
	waitOperationCodeQueryLock(t, pool, "LockExpiredRevisionPinApps")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-finished:
		if result.err != nil || result.count != 0 {
			t.Fatalf("cleanup used its snapshot from before the lock: %d %v", result.count, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not resume after reference publication")
	}
	requireRetainedOperationCode(t, s, op)
}

func TestPgOperationCodeCleanupIsPaged(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, _, app, _, _, _ := operationFixture(t, s)
	if _, err := pool.Exec(ctx, `WITH inserted AS (
INSERT INTO deployments(id,app_id,kind,image_digest,status,scope,traffic_percent,traffic_percent_explicit)
SELECT md5('operation-code-page-'||n::text)::uuid,$1,'image','sha256:page','live','default',0,true
FROM generate_series(1,$2::integer) n RETURNING id,app_id)
INSERT INTO deployment_revision_pins(deployment_id,app_id,expires_at)
SELECT id,app_id,now()-interval '1 hour' FROM inserted`, app.ID, api.RevisionPinCleanupPageMax+1); err != nil {
		t.Fatal(err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != api.RevisionPinCleanupPageMax {
		t.Fatalf("first cleanup exceeded or lost its page: %d %v", count, err)
	}
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 1 {
		t.Fatalf("remaining cleanup page: %d %v", count, err)
	}
}

func waitOperationCodeQueryLock(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
AND wait_event_type='Lock' AND query LIKE $1)`, "%-- name: "+name+" %").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not serialize on the app lock", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertGraphAdmissionRechecksAfterTargetLock(t *testing.T, pool *pgxpool.Pool, s *state.PgStore, op state.Operation,
	def state.OperationDefinition, tenant state.PlatformTenant, target state.Deployment) {
	t.Helper()
	ctx := t.Context()
	// Exercise the graph-lock race with a valid public admission window. The
	// expired-window assertions above already require rejection before admission.
	var originalExpiry time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM project_release_sets WHERE id=$1`, op.ReleaseID).Scan(&originalExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at=now()+interval '1 hour' WHERE id=$1`, op.ReleaseID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET expires_at=$2 WHERE id=$1`, op.ReleaseID, originalExpiry); err != nil {
			t.Error(err)
		}
	}()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Retirement follows the account -> app -> deployment lock order used by
	// lifecycle invalidation. Admission must recheck the graph after that
	// account fence is released, rather than deadlock on an inverted order.
	var id string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM accounts WHERE id=$1 FOR UPDATE`, op.AccountID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT id::text FROM apps WHERE id=$1 FOR UPDATE`, target.AppID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: op.AccountID, DefinitionID: def.ID,
			PlatformTenantID: tenant.ID, IdempotencyKey: "target-retirement-race", Input: []byte(`{"count":1}`)})
		finished <- err
	}()
	waitOperationCodeQueryLock(t, pool, "LockCustomerOperationAccount")
	if _, err := tx.Exec(ctx, `UPDATE deployments SET status='superseded' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("admission ignored a retired release member: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admission did not resume after release member retirement")
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='live' WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
}
