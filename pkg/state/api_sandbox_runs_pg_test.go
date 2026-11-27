package state_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxRunRecoversInterruptedLeaseAndPreservesReport(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "scenario-run@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "scenario-source", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := store.PutAPISandboxScenario(ctx, source.ID, api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`), APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Run fixture", Requests: []api.APISandboxExampleRequest{{Title: "Write fixture", Method: "POST", Path: "/notes", ExpectedStatus: 201}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	expiry := time.Now().UTC().Add(time.Hour)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: acct.ID, Slug: "api-sbx-pg-run", RAMMB: 512, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry, APISandboxRunID: id, APISandboxScenario: &scenario,
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: source.ID, APISandboxRun: true},
	}, api.MustLimitsFor(acct.Plan))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.ClaimAPISandboxRun(ctx, now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("run was claimable before verification: %v", err)
	}
	op, err := store.ClaimAPISandboxOperation(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op.Phase, op.NextAttemptAt, op.DeploymentID = state.APISandboxLive, nil, uuid.NewString()
	op.Verification = &api.APISandboxVerification{DeploymentID: op.DeploymentID, CheckedAt: now, OpenAPIStatus: 200}
	if err := store.SaveAPISandboxOperation(ctx, op, now); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimAPISandboxRun(ctx, now, time.Minute)
	if err != nil || claim.ID != id || claim.Interrupted {
		t.Fatalf("claim: %+v (%v)", claim, err)
	}
	claim.SandboxDeploymentID = op.DeploymentID
	step := api.APISandboxRunStep{Index: 1, Method: "POST", Title: "Write fixture", ExpectedStatus: 201, HTTPStatus: 201}
	if err := store.AppendAPISandboxRunStep(ctx, claim, step, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAPISandboxRun(ctx, now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second worker claimed an active lease: %v", err)
	}
	later := now.Add(2 * time.Minute)
	recovery, err := store.ClaimAPISandboxRun(ctx, later, time.Minute)
	if err != nil || !recovery.Interrupted || recovery.LeaseToken == claim.LeaseToken || len(recovery.Steps) != 1 {
		t.Fatalf("interrupted run lost its checkpoint: %+v (%v)", recovery, err)
	}
	if err := store.AppendAPISandboxRunStep(ctx, claim, step, later); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker wrote a checkpoint: %v", err)
	}
	recovery.Status = api.APISandboxRunInterrupted
	recovery.FailureCode = "worker_interrupted"
	if err := store.FinishAPISandboxRun(ctx, recovery, later); err != nil {
		t.Fatal(err)
	}
	closed, err := store.AppByID(ctx, app.ID)
	if err != nil || closed.PreviewPrState != state.PreviewPrStateTearingDown {
		t.Fatalf("run finish didn't close the sandbox: %v", err)
	}
	hash := sha256.Sum256([]byte("report fixture"))
	if err := store.ShareAPISandboxRun(ctx, id, hash[:], later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetAPISandboxRun(ctx, id)
	if err != nil || saved.Status != api.APISandboxRunInterrupted || saved.ShareExpiresAt == nil ||
		len(saved.Steps) != 1 || saved.Steps[0].HTTPStatus != 201 || saved.LeaseToken != "" {
		t.Fatalf("report was not persisted: %+v (%v)", saved, err)
	}
}

func TestPgAPISandboxPartnerRunFencesRequestsAndClosesOnFailure(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "partner-run@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "partner-run-source", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := store.PutAPISandboxScenario(ctx, source.ID, api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{Name: "Partner refund", Requests: []api.APISandboxExampleRequest{
			{Title: "Create refund", Method: "POST", Path: "/refunds?fixture=hidden", ExpectedStatus: 201},
			{Title: "Complete refund", Method: "PATCH", Path: "/refunds/1", ExpectedStatus: 204},
			{Title: "Verify order", Method: "GET", Path: "/orders/1", ExpectedStatus: 200},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	expiry := time.Now().UTC().Add(time.Hour)
	app, err := store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: acct.ID, Slug: "api-sbx-partner-run", RAMMB: 512, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry, APISandboxRunID: id, APISandboxScenario: &scenario,
		APISandboxPartnerLabel: "Partner", Manifest: state.AppManifest{APISandboxSourceDeploymentID: source.ID, APISandboxRun: true, APISandboxPartner: true},
	}, api.MustLimitsFor(acct.Plan))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	op, err := store.ClaimAPISandboxOperation(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op.Phase, op.NextAttemptAt, op.DeploymentID = state.APISandboxLive, nil, uuid.NewString()
	op.Verification = &api.APISandboxVerification{DeploymentID: op.DeploymentID, CheckedAt: now, OpenAPIStatus: 200}
	if err := store.SaveAPISandboxOperation(ctx, op, now); err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimAPISandboxRun(ctx, now, time.Minute)
	if err != nil || run.Mode != api.APISandboxRunModePartner {
		t.Fatalf("claim partner run: %+v (%v)", run, err)
	}
	run.SandboxDeploymentID = op.DeploymentID
	if err := store.OpenAPISandboxPartnerRun(ctx, run, now); err != nil {
		t.Fatal(err)
	}

	consumer, err := store.CreateAPIConsumer(ctx, acct.ID, app.ID, api.APISandboxPartnerConsumerRef, "Partner")
	if err != nil {
		t.Fatal(err)
	}
	invite := sha256.Sum256([]byte("invite"))
	if _, err := store.SetAPISandboxPartnerInvite(ctx, app.ID, invite[:], now); err != nil {
		t.Fatal(err)
	}
	_, prefix, hash, err := api.GenerateConsumerKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.RedeemAPISandboxPartnerInvite(ctx, state.APISandboxPartnerRedemption{
		App: app, ConsumerID: consumer.ID, KeyName: uuid.NewString(), Prefix: prefix,
		InviteHash: invite[:], KeyHash: hash, At: now, Quota: state.ConsumerKeyQuota{PerApp: 2, PerAccount: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAPISandboxRun(ctx, now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("worker could run final verification early: %v", err)
	}
	if _, err := store.ClaimAPISandboxPartnerStep(ctx, app.ID, op.DeploymentID, key.ID, "PATCH", "/refunds/1", now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("out-of-order step accepted: %v", err)
	}
	if _, err := store.ClaimAPISandboxPartnerStep(ctx, app.ID, uuid.NewString(), key.ID, "POST", "/refunds?fixture=hidden", now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("non-pinned release accepted: %v", err)
	}
	type claimResult struct {
		step api.APISandboxPartnerStep
		err  error
	}
	claims := make(chan claimResult, 2)
	for range 2 {
		go func() {
			step, err := store.ClaimAPISandboxPartnerStep(ctx, app.ID, op.DeploymentID, key.ID, "POST", "/refunds?fixture=hidden", now)
			claims <- claimResult{step, err}
		}()
	}
	var permit api.APISandboxPartnerStep
	for range 2 {
		result := <-claims
		switch {
		case result.err == nil && permit.Token == "":
			permit = result.step
		case errors.Is(result.err, state.ErrNotFound):
		default:
			t.Fatalf("unexpected concurrent claim: %v", result.err)
		}
	}
	if permit.Token == "" || permit.Step.Index != 1 || permit.Step.Path != "/refunds" {
		t.Fatalf("unusable or unredacted step: %+v", permit.Step)
	}
	permit.Step.HTTPStatus = 201
	if err := store.CompleteAPISandboxPartnerStep(ctx, permit, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAPISandboxPartnerStep(ctx, permit, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale claim appended a second result: %v", err)
	}

	permit, err = store.ClaimAPISandboxPartnerStep(ctx, app.ID, op.DeploymentID, key.ID, "PATCH", "/refunds/1", now)
	if err != nil || permit.Step.Index != 2 {
		t.Fatalf("claim second request: %+v (%v)", permit, err)
	}
	permit.Step.HTTPStatus, permit.Step.FailureCode = 422, "unexpected_http_status"
	if err := store.CompleteAPISandboxPartnerStep(ctx, permit, now); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetAPISandboxRun(ctx, id)
	if err != nil || saved.Status != api.APISandboxRunFailed || len(saved.Steps) != 2 || saved.Steps[1].HTTPStatus != 422 {
		t.Fatalf("partner checkpoint lost: %+v (%v)", saved, err)
	}
	closed, err := store.AppByID(ctx, app.ID)
	if err != nil || closed.PreviewPrState != state.PreviewPrStateTearingDown {
		t.Fatalf("failed run left the sandbox open: %v", err)
	}
}
