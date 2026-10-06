// adr: 521
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationPublicWindowStore interface {
	operationLifecycleStore
	operationPinFixture
	UpdateApp(context.Context, string, state.UpdateAppParams) (state.App, error)
}

func testOperationPrivatePinsPreservePublicWindow(t *testing.T, s operationPublicWindowStore) {
	t.Helper()
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 1
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "public-window", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.CreateDeployment(ctx, state.Deployment{Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:replacement"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, next.ID); err != nil {
		t.Fatal(err)
	}
	// Observe the configured public TTL through the real resolver. An operation
	// result window must never turn this one-second grant into a multi-day grant.
	deadline := time.Now().Add(4 * time.Second)
	for {
		_, err := s.ResolveRevisionPin(ctx, app.ID, def.Scope, def.DeploymentID)
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("operation admission extended the configured public revision window")
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertPublicExpired := func(stage string) {
		t.Helper()
		if _, err := s.ResolveRevisionPin(ctx, app.ID, def.Scope, def.DeploymentID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("%s resurrected the public revision grant: %v", stage, err)
		}
		dep, err := s.DeploymentByID(ctx, def.DeploymentID)
		if err != nil || dep.Status != state.DeployLive || dep.TrafficPercent != 0 {
			t.Fatalf("%s lost privately retained code: %+v %v", stage, dep, err)
		}
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicExpired("claim")
	var headers map[string]string
	if err := json.Unmarshal(inv.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	authority := state.OperationExecutionAuthority{AccountID: acct.ID, AppID: app.ID, InstanceID: instance.ID,
		InvocationID: inv.ID, Attempt: inv.Attempts, Capability: headers[api.OperationCapabilityHeader]}
	if _, err := s.ReportOperationProgress(ctx, op.ID, authority, api.OperationReportRequest{
		ReportID: "one", Stage: "generating", Completed: 1, Total: 2}); err != nil {
		t.Fatal(err)
	}
	assertPublicExpired("progress")
	if err := s.FailInvocation(ctx, inv.ID, "lost response", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	assertPublicExpired("reconciliation")
	recovered, err := s.RecoverOperation(ctx, acct.ID, tenant.ID, op.ID, api.OperationRecoveryRequest{
		RecoveryID: "verified-retry", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified no export or external effect exists"})
	if err != nil {
		t.Fatal(err)
	}
	assertPublicExpired("safe recovery")
	retry, err := s.ClaimInvocationWithCap(ctx, recovered.CurrentInvocationID, instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteKeyedInvocation(ctx, retry.ID, retry.Attempts, []byte(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	assertPublicExpired("completion")
	if count, err := s.ExpireRevisionPins(ctx); err != nil || count != 0 {
		t.Fatalf("expired public grant retired retained result/recovery code: %d %v", count, err)
	}
}

func TestMemOperationPrivatePinsPreservePublicWindow(t *testing.T) {
	testOperationPrivatePinsPreservePublicWindow(t, state.NewMemStore())
}

func TestPgOperationPrivatePinsPreservePublicWindow(t *testing.T) {
	s, _ := pgStore(t)
	testOperationPrivatePinsPreservePublicWindow(t, s)
}

func TestPgOperationPrivatePinRenewalRacingWithCleanup(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, acct, app, def, tenant, _ := operationFixture(t, s)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, DefinitionID: def.ID,
		PlatformTenantID: tenant.ID, IdempotencyKey: "private-renewal-race", Input: []byte(`{"count":1}`)})
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
	// A save updates only the existing private receipt. Cleanup's delete must
	// recheck that row after waiting, and removing an expired public receipt
	// must not retire code when the private receipt was renewed meanwhile.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE customer_operation_code_pins SET expires_at=now()+interval '1 hour' WHERE deployment_id=$1`, op.DeploymentID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		count, err := s.ExpireRevisionPins(ctx)
		if err == nil && count != 0 {
			err = errors.New("cleanup retired code after its private receipt was renewed")
		}
		finished <- err
	}()
	waitOperationCodeQueryLock(t, pool, "ExpireRetainedDeploymentRevisionPins")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not resume after private receipt renewal")
	}
	dep, err := s.DeploymentByID(ctx, op.DeploymentID)
	if err != nil || dep.Status != state.DeployLive || dep.TrafficPercent != 0 {
		t.Fatalf("renewed private code was retired: %+v %v", dep, err)
	}
	if _, err := s.ResolveRevisionPin(ctx, app.ID, def.Scope, op.DeploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("private renewal resurrected expired public grant: %v", err)
	}
}
