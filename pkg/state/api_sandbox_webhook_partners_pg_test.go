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

func TestPgAPISandboxWebhookPartnerPersistsAnImmutableOutcome(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "receiver-setup@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "receiver-setup-source", RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateDeployment(ctx, state.Deployment{AppID: parent.ID, Kind: state.DeploymentKindTarball, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := store.PutAPISandboxScenario(ctx, source.ID, api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`), OutboundWebhook: &api.APISandboxOutboundWebhook{},
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "Partner callback", Requests: []api.APISandboxExampleRequest{
				{Title: "Start job", Method: "POST", Path: "/jobs", ExpectedStatus: 202},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id, expiry := uuid.NewString(), now.Add(time.Hour)
	inviteHash, outcomeHash := sha256.Sum256([]byte("pgsql invite")), sha256.Sum256([]byte("pgsql outcome"))
	partner := state.APISandboxWebhookPartner{
		RunID: id, SourceDeploymentID: source.ID, ScenarioRevision: scenario.Revision, ExpiresAt: expiry,
		InviteHash: inviteHash[:],
	}
	if err := store.CreateAPISandboxWebhookPartner(ctx, partner); err != nil {
		t.Fatal(err)
	}
	partner.TargetURL, partner.OutcomeTokenHash, partner.SigningSecretSealed = "https://receiver.example.test/callback", outcomeHash[:], []byte("sealed")
	if err := store.RedeemAPISandboxWebhookPartner(ctx, partner, now); err != nil {
		t.Fatal(err)
	}
	if err := store.RedeemAPISandboxWebhookPartner(ctx, partner, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("redeemed invite twice: %v", err)
	}
	if err := store.ReadyAPISandboxWebhookPartner(ctx, id, outcomeHash[:], now); err != nil {
		t.Fatal(err)
	}
	setups, err := store.ListAPISandboxWebhookPartnerSetups(ctx, source.ID, now)
	if err != nil || len(setups) != 1 || setups[0].Status != "ready" || setups[0].Token != "" {
		t.Fatalf("unexpected ready setup: %+v (%v)", setups, err)
	}
	_, err = store.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: acct.ID, Slug: "api-sbx-pg-receiver", RAMMB: 512, PreviewOfSlug: state.APISandboxPreviewParent(parent.Slug),
		PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry,
		APISandboxRunID: id, APISandboxScenario: &scenario, APISandboxRunPartnerWebhook: true,
		Manifest: state.AppManifest{APISandboxSourceDeploymentID: source.ID, APISandboxRun: true},
	}, api.MustLimitsFor(acct.Plan))
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC()
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
	if err != nil || !run.PartnerWebhook {
		t.Fatalf("partner run was not claimed: %v", err)
	}
	run.SandboxDeploymentID = op.DeploymentID
	step := api.APISandboxRunStep{Index: 1, Title: "Deliver", Method: "POST", Kind: api.APISandboxStepOutboundAttempt, AttemptNumber: 2, DeliveryID: uuid.NewString()}
	if err := store.AppendAPISandboxRunStep(ctx, run, step, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ParkAPISandboxWebhookPartnerRun(ctx, run, now, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if run, err := store.GetAPISandboxRun(ctx, id); err != nil || run.Status != api.APISandboxRunAwaitingOutcome || run.LeaseUntil != nil {
		t.Fatalf("parked run retained a worker lease or lost its status: %+v (%v)", run, err)
	}
	if _, err := store.ClaimAPISandboxRun(ctx, now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("run was claimable before receiving evidence: %v", err)
	}
	if err := store.RecordAPISandboxWebhookPartnerOutcome(ctx, id, inviteHash[:], 1, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unexpected outcome authority: %v", err)
	}
	for range 2 {
		if err := store.RecordAPISandboxWebhookPartnerOutcome(ctx, id, outcomeHash[:], 0, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RecordAPISandboxWebhookPartnerOutcome(ctx, id, outcomeHash[:], 1, now); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("committed zero outcome was overwritten: %v", err)
	}
	run, err = store.ClaimAPISandboxRun(ctx, now, time.Minute)
	if err != nil || run.PartnerOutcomeUntil == nil || run.Interrupted || len(run.Steps) != 1 {
		t.Fatalf("run was not claimable after receiving evidence: %+v (%v)", run, err)
	}
	current, err := store.GetAPISandboxWebhookPartner(ctx, id)
	if err != nil || current.OutcomeAt == nil || current.OutcomeCount != 0 || current.InviteHash != nil {
		t.Fatalf("partner evidence was not persisted, err=%v", err)
	}
}
