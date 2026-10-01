package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func invocationScopeApp(t *testing.T, fx *Fixture) (state.Project, state.App) {
	t.Helper()
	project, err := fx.Store.CreateProject(fx.Ctx, state.Project{AccountID: fx.Account.ID, Slug: "scope-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, ProjectID: project.ID,
		Slug: "scope-api-" + uuid.NewString()[:8], Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return project, app
}

func testInvocationDeploymentScope(t *testing.T, fx *Fixture) {
	project, app := invocationScopeApp(t, fx)
	enqueue := func(scope string) state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
			DeploymentScope: scope, Source: state.InvocationQueue, Method: "POST", Path: "/", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	production, staging := enqueue(""), enqueue("staging")
	if production.DeploymentScope != "production" || staging.DeploymentScope != "staging" {
		t.Fatalf("admitted scopes = %q, %q", production.DeploymentScope, staging.DeploymentScope)
	}
	for _, scope := range []string{"__all__", "UPPER", "-staging", "a", "staging/other"} {
		if _, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
			DeploymentScope: scope, Source: state.InvocationQueue, DueAt: time.Now()}); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid scope %q accepted: %v", scope, err)
		}
	}
	if err := fx.Store.DeleteProject(fx.Ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	if app, err := fx.Store.AppByID(fx.Ctx, app.ID); err != nil || app.ProjectID != "" {
		t.Fatalf("project removal = %+v, %v", app, err)
	}
	if got := enqueue(""); got.DeploymentScope != api.DefaultEnvScope {
		t.Fatalf("new invocation after removal = %q", got.DeploymentScope)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, production.ID, "", 30, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, staging.ID, "", 30, 100); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("second scope bypassed stored account cap: %v", err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, production.ID, "retry", time.Nanosecond, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, staging.ID, "", 30, 1); err != nil {
		t.Fatalf("retry failed to release shared capacity: %v", err)
	}
	if err := fx.Store.CompleteInvocation(fx.Ctx, staging.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, production.ID, "", 30, 1); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, production.ID, "exhausted", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	replayed, err := fx.Store.RetryQueueDeadLetter(fx.Ctx, fx.Account.ID, production.ID)
	if err != nil || replayed.DeploymentScope != "production" || replayed.State != state.InvocationPending {
		t.Fatalf("DLQ replay moved environment: %+v, %v", replayed, err)
	}
	for _, want := range []state.Invocation{production, staging} {
		got, err := fx.Store.InvocationByID(fx.Ctx, want.ID)
		if err != nil || got.DeploymentScope != want.DeploymentScope {
			t.Fatalf("stored scope = %q, want %q: %v", got.DeploymentScope, want.DeploymentScope, err)
		}
		_, selected, err := state.ResolveInvocationVersion(fx.Ctx, fx.Store, got)
		if err != nil || selected.Scope != want.DeploymentScope {
			t.Fatalf("delivery changed scope: %+v, %v", selected, err)
		}
	}
}

func testKeyedInvocationDeploymentScope(t *testing.T, fx *Fixture) {
	project, app := invocationScopeApp(t, fx)
	request := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: fx.Account.ID,
		DeploymentScope: "staging", Source: state.InvocationQueue, Method: "POST", Path: "/", DueAt: time.Now()}
	policy := workpolicy.Policy{Name: "updates", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	key, err := workpolicy.CanonicalScalar([]byte(`"document-1"`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, request, policy, key)
	if err != nil || first.DeploymentScope != "staging" {
		t.Fatalf("keyed admission: %+v, %v", first, err)
	}
	if err := fx.Store.DeleteProject(fx.Ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	request.DeploymentScope = ""
	got, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, request, policy, key)
	if err != nil || got.ID != first.ID || got.DeploymentScope != "staging" {
		t.Fatalf("idempotent request moved environment: %+v, %v", got, err)
	}
	request.DeploymentScope = "production"
	if _, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, request, policy, key); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("idempotent scope replacement accepted: %v", err)
	}
}

func testQueueBatchClaimAdmission(t *testing.T, fx *Fixture) {
	_, app := invocationScopeApp(t, fx)
	enqueue := func(source state.InvocationSource) state.Invocation {
		t.Helper()
		row, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
			DeploymentScope: "staging", Source: source, Method: "POST", Path: "/stored", DueAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	wire := func(row state.Invocation) state.Invocation {
		return state.Invocation{ID: row.ID, AppID: app.ID, Source: "esm", Attempts: row.Attempts,
			Method: "POST", Path: "/_triggers/esm/test", Payload: []byte(`{"job":"work"}`)}
	}
	for _, source := range []state.InvocationSource{state.InvocationQueue, state.InvocationDelayedTask} {
		t.Run(string(source), func(t *testing.T) {
			pending := enqueue(source)
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, wire(pending)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("pending invocation admitted: %v", err)
			}
			first, err := fx.Store.ClaimInvocation(fx.Ctx, pending.ID, "", 30)
			if err != nil {
				t.Fatal(err)
			}
			envelope := wire(first)
			admitted, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, envelope)
			if err != nil || admitted.DeploymentScope != "staging" || admitted.Path != envelope.Path || admitted.Source != "esm" {
				t.Fatalf("batch admission: scope=%q path=%q source=%q err=%v", admitted.DeploymentScope, admitted.Path, admitted.Source, err)
			}
			for _, tc := range []struct {
				name, appID, scope string
				attempt            int
			}{
				{"missing attempt", app.ID, "", 0},
				{"future attempt", app.ID, "", first.Attempts + 1},
				{"foreign app", uuid.NewString(), "", first.Attempts},
				{"foreign scope", app.ID, "production", first.Attempts},
			} {
				bad := envelope
				bad.Attempts, bad.DeploymentScope = tc.attempt, tc.scope
				if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, tc.appID, bad); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("%s admitted: %v", tc.name, err)
				}
			}
			if err := fx.Store.FailInvocation(fx.Ctx, first.ID, "retry", time.Nanosecond, 3, state.WithClaimAttempt(first.Attempts)); err != nil {
				t.Fatal(err)
			}
			second, err := fx.Store.ClaimInvocation(fx.Ctx, first.ID, "", 30)
			if err != nil || second.Attempts != first.Attempts+1 {
				t.Fatalf("replacement claim: attempt=%d err=%v", second.Attempts, err)
			}
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, envelope); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale attempt admitted: %v", err)
			}
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, wire(second)); err != nil {
				t.Fatalf("current attempt refused: %v", err)
			}
			if err := fx.Store.CompleteInvocation(fx.Ctx, second.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, wire(second)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("completed invocation admitted: %v", err)
			}
			expired, err := fx.Store.ClaimInvocation(fx.Ctx, enqueue(source).ID, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, wire(expired)); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("expired lease admitted: %v", err)
			}
		})
	}
	async, err := fx.Store.ClaimInvocation(fx.Ctx, enqueue(state.InvocationAsyncInvoke).ID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, wire(async)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("non-queue invocation admitted as queue work: %v", err)
	}
	missing := state.Invocation{ID: uuid.NewString(), AppID: app.ID, Source: "esm", Attempts: 1}
	if _, err := state.AdmitPlatformTenantInvocation(fx.Ctx, fx.Store, app.ID, missing); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing durable invocation admitted: %v", err)
	}
}
