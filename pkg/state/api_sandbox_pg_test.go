package state_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxUsesRetainedReleaseArtifact(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "api-sandbox-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "sandbox-pg-source", RAMMB: 512, Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := []string{"python", "migrate.py"}
	source, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive, ReleaseCommand: command,
	})
	if err != nil {
		t.Fatal(err)
	}
	const root = "sandbox-pg-fixture.ext4"
	if err := store.SetDeploymentRootfs(ctx, source.ID, "/apps/"+root, root, 1024); err != nil {
		t.Fatal(err)
	}
	// This fixture represents an already completed production migration.
	if _, err := pool.Exec(ctx, "update deployments set status = 'live' where id = $1", source.ID); err != nil {
		t.Fatal(err)
	}
	source, err = store.DeploymentByID(ctx, source.ID)
	if err != nil || !state.APISandboxSourceSupported(source) {
		t.Fatalf("source fixture is ineligible: status=%s, layer=%q, error=%v", source.Status, source.RootfsKey, err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, parent.ID, api.DefaultEnvScope, "PRODUCTION_VALUE", "private"); err != nil {
		t.Fatal(err)
	}
	definition := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{"notes":[{"id":42,"body":"fixture"}]}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Scenario fixture", SeededIDs: map[string]string{"Note": "42"},
			Requests: []api.APISandboxExampleRequest{{Title: "List notes", Method: "GET", Path: "/notes", ExpectedStatus: 200}},
		},
	}
	origin := state.APISandboxScenarioOrigin{RequestID: uuid.NewString()}
	saved, err := store.PutAPISandboxScenarioFromRequest(ctx, source.ID, definition, origin)
	if err != nil || saved.Revision == "" || saved.SourceRequestID != origin.RequestID {
		t.Fatalf("save scenario: %+v (%v)", saved, err)
	}
	if _, err := store.PutAPISandboxScenarioFromRequest(ctx, source.ID, definition, origin); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale empty revision returned %v", err)
	}
	if again, err := store.PutAPISandboxScenario(ctx, source.ID, definition); err != nil || again.Revision != saved.Revision {
		t.Fatalf("retry changed revision: %+v (%v)", again, err)
	}
	expiry := time.Now().Add(time.Hour)
	sandbox, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID, Slug: "api-sbx-postgres-artifact", RAMMB: 512, Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassHTTP, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry, APISandboxSmokePath: "/notes", APISandboxScenario: &saved,
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: source.ID},
	}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	if !state.APISandboxAcceptsRequests(sandbox, time.Now()) {
		t.Fatalf("sandbox lease was not persisted: status=%s, preview=%q, state=%s, lease=%v, source=%q",
			sandbox.Status, sandbox.PreviewOfSlug, sandbox.PreviewPrState, sandbox.PreviewExpiresAt, sandbox.Manifest.APISandboxSourceDeploymentID)
	}
	definition.Seed = json.RawMessage(`{"notes":[]}`)
	current, err := store.PutAPISandboxScenario(ctx, source.ID, definition)
	if err != nil || current.Revision == saved.Revision {
		t.Fatalf("editing scenario failed: %+v (%v)", current, err)
	}
	if loaded, err := store.GetAPISandboxScenario(ctx, source.ID); err != nil || loaded.Revision != current.Revision || loaded.SourceRequestID != origin.RequestID {
		t.Fatalf("latest scenario = %+v (%v)", loaded, err)
	}
	origin.ExpectedRevision = saved.Revision
	if _, err := store.PutAPISandboxScenarioFromRequest(ctx, source.ID, definition, origin); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale nonempty revision returned %v", err)
	}
	newOrigin := state.APISandboxScenarioOrigin{RequestID: uuid.NewString(), ExpectedRevision: current.Revision}
	replaced, err := store.PutAPISandboxScenarioFromRequest(ctx, source.ID, definition, newOrigin)
	if err != nil || replaced.Revision == current.Revision || replaced.SourceRequestID != newOrigin.RequestID {
		t.Fatalf("replace provenance: %+v (%v)", replaced, err)
	}
	now := time.Now().UTC()
	claimed, err := store.ClaimAPISandboxOperation(ctx, now, time.Minute)
	if err != nil || claimed.AppID != sandbox.ID || claimed.Phase != state.APISandboxPreparingDatabase ||
		claimed.SmokePath != "/notes" || claimed.LeaseToken == "" || claimed.LeaseUntil == nil ||
		claimed.Scenario == nil || claimed.Scenario.Revision != saved.Revision || string(claimed.Scenario.Seed) != string(saved.Seed) ||
		claimed.Scenario.SourceRequestID != "" {
		t.Fatalf("reservation did not enqueue a claimable operation: %+v (%v)", claimed, err)
	}

	// A restarted worker can reclaim after the lease expires. The old
	// worker's checkpoint must be refused, even if it later wakes up.
	restarted := state.NewPgStore(pool)
	if _, err := restarted.ClaimAPISandboxOperation(ctx, now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a second worker claimed an active lease: %v", err)
	}
	later := now.Add(2 * time.Minute)
	reclaimed, err := restarted.ClaimAPISandboxOperation(ctx, later, time.Minute)
	if err != nil || reclaimed.AppID != claimed.AppID || reclaimed.LeaseToken == claimed.LeaseToken {
		t.Fatalf("sandbox lease did not recover: %+v (%v)", reclaimed, err)
	}
	if err := store.SaveAPISandboxOperation(ctx, claimed, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired worker saved a checkpoint: %v", err)
	}
	deployment, err := store.CreateAPISandboxDeployment(ctx, account.ID, sandbox.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Status != state.DeploySnapshotting || deployment.RootfsKey != root ||
		deployment.AppID != sandbox.ID || !slices.Equal(deployment.ReleaseCommand, command) {
		t.Fatalf("sandbox did not stage the exact artifact: %+v", deployment)
	}
	reclaimed.Phase, reclaimed.DeploymentID = state.APISandboxLive, deployment.ID
	reclaimed.Verification = &api.APISandboxVerification{DeploymentID: deployment.ID, CheckedAt: later, OpenAPIStatus: 200, SmokeStatus: 204}
	reclaimed.NextAttemptAt = nil
	if err := restarted.SaveAPISandboxOperation(ctx, reclaimed, later); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.GetAPISandboxOperation(ctx, sandbox.ID)
	if err != nil || receipt.Phase != state.APISandboxLive || receipt.LeaseUntil != nil || receipt.LeaseToken != "" ||
		receipt.Verification == nil || receipt.Verification.DeploymentID != deployment.ID || receipt.Verification.SmokeStatus != 204 ||
		receipt.Scenario == nil || receipt.Scenario.Revision != saved.Revision || receipt.Scenario.SeededIDs["Note"] != "42" {
		t.Fatalf("persisted verification receipt = %+v (%v)", receipt, err)
	}
	if _, err := restarted.ClaimAPISandboxOperation(ctx, later, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("terminal operation remained claimable: %v", err)
	}
	if env, err := store.ListAppEnvInScope(ctx, account.ID, sandbox.ID, api.DefaultEnvScope); err != nil || len(env) != 0 {
		t.Fatalf("sandbox copied production runtime values: %v (%v)", env, err)
	}
	if again, err := store.CreateAPISandboxDeployment(ctx, account.ID, sandbox.ID, source.ID); err != nil || again.ID != deployment.ID {
		t.Fatalf("resume changed deployment: %s (%v)", again.ID, err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: sandbox.ID, Kind: state.DeploymentKindImage}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("sandbox accepted an unrelated deployment: %v", err)
	}

	t.Run("webhook dispatch stays on the isolated deployment", func(t *testing.T) {
		runApp, err := store.CreateAppIfUnderQuota(ctx, state.App{
			AccountID: account.ID, Slug: "api-sbx-postgres-webhook", RAMMB: 512, Type: state.AppTypeApp,
			WorkloadClass: state.WorkloadClassHTTP, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry,
			Manifest: state.AppManifest{APISandboxSourceDeploymentID: source.ID, APISandboxRun: true},
		}, api.MustLimitsFor(account.Plan))
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateAPISandboxDeployment(ctx, account.ID, runApp.ID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		headers, err := json.Marshal(map[string]string{api.RevisionHeader: dep.ID})
		if err != nil {
			t.Fatal(err)
		}
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: runApp.ID, AccountID: account.ID, Source: state.InvocationInboundWebhook,
			Method: "POST", Path: "/internal/stripe", Payload: json.RawMessage(`{"id":"evt_demo"}`),
			Headers: headers, DueAt: time.Now().UTC(),
			AdmissionMaxPending: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		inv, err = store.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, version, err := state.ResolveInvocationVersion(ctx, store, inv); err != nil || version.DeploymentID != dep.ID {
			t.Fatalf("stored webhook resolved to %+v (%v)", version, err)
		}
		misrouted := inv
		misrouted.Headers, _ = json.Marshal(map[string]string{api.RevisionHeader: source.ID})
		if _, _, err := state.ResolveInvocationVersion(ctx, store, misrouted); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("sandbox could route to the source deployment: %v", err)
		}
		hook, err := store.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
			Scope: state.AppWebhookScopeApp, AppID: runApp.ID, AccountID: account.ID, Enabled: true,
			TargetURL: api.APISandboxWebhookReceiverPrefix + dep.ID, SecretSealed: []byte("sealed fixture"),
			EventFilter: []string{string(state.AppWebhookEventJobFinished)},
		}, api.MustLimitsFor(account.Plan))
		if err != nil {
			t.Fatal(err)
		}
		external := "https://external.example.test/callback"
		if _, err := store.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{TargetURL: &external}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("reserved receiver could be redirected: %v", err)
		}
		job, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: runApp.ID, AccountID: account.ID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/exports", Headers: headers, DueAt: time.Now().UTC(),
			OnSuccessDestinationID: hook.ID, AdmissionMaxPending: 10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, version, err := state.ResolveInvocationVersion(ctx, store, job); err != nil || version.DeploymentID != dep.ID {
			t.Fatalf("async job did not resolve to the isolated deployment: %+v (%v)", version, err)
		}
		claim, err := store.ClaimInvocation(ctx, job.ID, "", 30)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteInvocationClaim(ctx, job.ID, state.InvocationClaim{
			Attempt: claim.Attempts, ReplayGeneration: claim.ReplayGeneration,
		}, json.RawMessage(`{"export_id":"demo"}`)); err != nil {
			t.Fatal(err)
		}
		deliveries, _, err := store.ListAppWebhookDeliveries(ctx, runApp.ID, hook.ID, 2, "")
		if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventJobFinished ||
			deliveries[0].Status != state.AppWebhookDeliveryPending {
			t.Fatalf("async job completion did not queue exactly one callback: %d (%v)", len(deliveries), err)
		}
		if _, err := store.SetPreviewPrState(ctx, runApp.ID, state.PreviewPrStateTearingDown); err != nil {
			t.Fatal(err)
		}
		if _, _, err := state.ResolveInvocationVersion(ctx, store, inv); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("closing sandbox still accepted webhook dispatch: %v", err)
		}
	})
}

func TestPgAPISandboxEgressCannotBeRelaxed(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, _, source := seedLiveDeploy(t, store, ctx, "sandbox-egress")
	expiry := time.Now().Add(time.Hour)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: accountID, Slug: "api-sbx-fixed-egress", RAMMB: 512, Type: state.AppTypeApp,
		PreviewOfSlug: state.APISandboxPreviewParent("pg-app"), PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry,
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: source},
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	if len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "127.0.0.1/32" || len(app.EgressPorts) != 0 {
		t.Fatalf("persisted sandbox egress = %v, %v", app.EgressAllowlist, app.EgressPorts)
	}
	_, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{SetEgressAllowlist: true})
	if !errors.Is(err, state.ErrAPISandboxEgressPolicy) {
		t.Fatalf("clearing the sandbox allowlist returned %v", err)
	}
	_, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{SetEgressPorts: true, EgressPorts: []int{5432}})
	if !errors.Is(err, state.ErrAPISandboxEgressPolicy) {
		t.Fatalf("adding sandbox egress ports returned %v", err)
	}
}
