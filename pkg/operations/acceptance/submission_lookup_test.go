// adr: 644
package acceptance_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func testOperationSubmissionLookup(t *testing.T, s operationFixtureStore) {
	t.Helper()
	ctx, account, app, def, alice, bob := operationFixture(t, s)
	lookup := s.(state.OperationSubmissionLookupStore)
	req := api.OperationSubmissionLookupRequest{AppID: app.ID, Scope: def.Scope, Name: def.Spec.Name, IdempotencyKey: "lost-acceptance"}
	missing, err := lookup.LookupPlatformTenantOperationSubmission(ctx, account.ID, alice.ID, req)
	if err != nil || missing.State != "unresolved" || missing.Receipt != nil {
		t.Fatal("missing submission", missing, err)
	}
	admission := state.OperationAdmission{AccountID: account.ID, PlatformTenantID: alice.ID, DefinitionID: def.ID, Input: []byte(`{"count":1}`), IdempotencyKey: req.IdempotencyKey,
		ExpectedIdentity: &api.OperationTenantIdentity{AccountID: account.ID, PlatformTenantID: alice.ID}, ExpectedScope: &api.OperationSubmissionScope{AppID: app.ID, Scope: def.Scope, Name: def.Spec.Name}}
	op, fresh, err := s.AdmitOperation(ctx, admission)
	if err != nil || !fresh {
		t.Fatal("acceptance", op, fresh, err)
	}
	// Simulate a dropped response. Concurrent reloads only observe the same
	// acceptance, leaving execution and event identity unchanged.
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := lookup.LookupPlatformTenantOperationSubmission(ctx, account.ID, alice.ID, req)
			if e != nil || got.State != "accepted" || got.Receipt == nil || got.Receipt.ID != op.ID || got.AcceptedAt == nil || !got.AcceptedAt.Equal(op.CreatedAt) || got.IdempotencyExpiresAt == nil {
				t.Errorf("lookup: %+v %v", got, e)
			}
		}()
	}
	wg.Wait()
	current, err := s.OperationByID(ctx, account.ID, alice.ID, op.ID)
	if err != nil || current.CurrentInvocationID != op.CurrentInvocationID || current.LatestSequence != op.LatestSequence || current.Generation != 1 {
		t.Fatal("lookup changed execution", current, err)
	}
	for _, tc := range []struct{ name, account, tenant, app, scope, operation, key string }{
		{"customer", account.ID, bob.ID, app.ID, def.Scope, def.Spec.Name, req.IdempotencyKey},
		{"account", uuid.NewString(), alice.ID, app.ID, def.Scope, def.Spec.Name, req.IdempotencyKey},
		{"app", account.ID, alice.ID, uuid.NewString(), def.Scope, def.Spec.Name, req.IdempotencyKey},
		{"environment", account.ID, alice.ID, app.ID, "staging", def.Spec.Name, req.IdempotencyKey},
		{"feature", account.ID, alice.ID, app.ID, def.Scope, "another-export", req.IdempotencyKey},
		{"key", account.ID, alice.ID, app.ID, def.Scope, def.Spec.Name, "another-key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := lookup.LookupPlatformTenantOperationSubmission(ctx, tc.account, tc.tenant, api.OperationSubmissionLookupRequest{AppID: tc.app, Scope: tc.scope, Name: tc.operation, IdempotencyKey: tc.key})
			if e != nil || got.State != "unresolved" || got.Receipt != nil {
				t.Fatal("foreign scope disclosed acceptance", got, e)
			}
		})
	}
	wrong := admission
	wrong.ExpectedIdentity = &api.OperationTenantIdentity{AccountID: account.ID, PlatformTenantID: bob.ID}
	if _, _, err = s.AdmitOperation(ctx, wrong); !errors.Is(err, state.ErrOperationIdentityConflict) {
		t.Fatal("rotated principal replayed", err)
	}
	req.ExpectedIdentity = wrong.ExpectedIdentity
	if _, err = lookup.LookupPlatformTenantOperationSubmission(ctx, account.ID, alice.ID, req); !errors.Is(err, state.ErrOperationIdentityConflict) {
		t.Fatal("rotated principal observed", err)
	}
	wrong = admission
	wrong.ExpectedScope = &api.OperationSubmissionScope{AppID: app.ID, Scope: "staging", Name: def.Spec.Name}
	if _, _, err = s.AdmitOperation(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatal("wrong feature replayed", err)
	}
	// A rollout does not move the original receipt to a new definition.
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: operationSpec()}}); err != nil {
		t.Fatal(err)
	}
	req.ExpectedIdentity = nil
	got, err := lookup.LookupPlatformTenantOperationSubmission(ctx, account.ID, alice.ID, req)
	if err != nil || got.Receipt == nil || got.Receipt.ID != op.ID {
		t.Fatal("rollout lost submission", got, err)
	}
}

func TestMemOperationSubmissionLookup(t *testing.T) {
	testOperationSubmissionLookup(t, state.NewMemStore())
}

func TestPgOperationSubmissionLookup(t *testing.T) {
	s, _ := pgStore(t)
	testOperationSubmissionLookup(t, s)
}

func TestPgOperationSubmissionLookupRetention(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	ctx, account, app, def, tenant, _ := operationFixture(t, s)
	op, _, err := s.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "retained", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	req := api.OperationSubmissionLookupRequest{AppID: app.ID, Scope: def.Scope, Name: def.Spec.Name, IdempotencyKey: "retained"}
	check := func(want string) {
		t.Helper()
		got, e := s.LookupPlatformTenantOperationSubmission(ctx, account.ID, tenant.ID, req)
		if e != nil || got.State != want {
			t.Fatal(want, got, e)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE customer_operation_idempotency SET expires_at=$2 WHERE operation_id=$1`, op.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	check("accepted") // Active work protects its key even past the nominal horizon.
	inv, err := s.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{"file":"export.csv"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE customer_operation_idempotency SET expires_at=$2 WHERE operation_id=$1`, op.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	check("expired")
	if _, err = pool.Exec(ctx, `UPDATE customer_operation_idempotency SET expires_at=$2 WHERE operation_id=$1`, op.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM customer_operations WHERE id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	check("expired") // Receipt outlives retained result.
	if _, err = pool.Exec(ctx, `DELETE FROM customer_operation_idempotency WHERE operation_id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	check("unresolved") // Pruning cannot prove the original request was rejected.
}

func TestBrowserReceiptReplayFitsPlanRetention(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		limits := api.MustLimitsFor(plan)
		if time.Duration(api.OperationBrowserReceiptReplaySeconds)*time.Second > time.Duration(limits.Operations.IdempotencyRetentionSeconds)*time.Second {
			t.Fatal("browser replay outlives idempotency retention", plan)
		}
	}
}
