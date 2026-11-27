package apidserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const apiSandboxRunLease = 90 * time.Second

func (s *server) runAPISandboxScenarios(ctx context.Context) {
	store, ok := s.store.(state.APISandboxRunStore)
	if !ok || !s.apiSandboxWorkerEnabled() {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	partners, _ := s.store.(state.APISandboxWebhookPartnerStore)
	var nextPartnerCleanup time.Time
	for {
		if now := time.Now().UTC(); partners != nil && !now.Before(nextPartnerCleanup) {
			cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
			if err := partners.DeleteExpiredAPISandboxWebhookPartners(cleanup, now); err != nil && ctx.Err() == nil {
				s.log.WarnContext(ctx, "expire sandbox webhook partner registrations", "err", err)
			}
			cancel()
			nextPartnerCleanup = now.Add(time.Minute)
		}
		if err := s.processAPISandboxRun(ctx, store); err != nil && !errors.Is(err, state.ErrNotFound) && ctx.Err() == nil {
			s.log.WarnContext(ctx, "process API sandbox scenario run", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) processAPISandboxRun(ctx context.Context, store state.APISandboxRunStore) error {
	now := time.Now().UTC()
	claimCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	run, err := store.ClaimAPISandboxRun(claimCtx, now, apiSandboxRunLease)
	cancel()
	if err != nil {
		return err
	}
	if run.PartnerInterrupted {
		run.Status, run.FailureCode = api.APISandboxRunInterrupted, "partner_request_interrupted"
	} else if run.Interrupted && run.PartnerOutcomeUntil == nil {
		// An earlier worker might have sent a mutating request just before it
		// stopped. Retain its checkpoints and never resume work on that copy.
		run.Status, run.FailureCode = api.APISandboxRunInterrupted, "worker_interrupted"
	} else {
		workCtx, stop := context.WithDeadline(ctx, run.LeaseUntil.Add(-5*time.Second))
		err := s.executeAPISandboxRun(workCtx, store, &run)
		stop()
		if err != nil {
			return err
		}
	}
	if run.Status == api.APISandboxRunAwaitingPartner || run.Status == api.APISandboxRunAwaitingOutcome {
		return nil
	}
	finishCtx, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	return store.FinishAPISandboxRun(finishCtx, run, time.Now().UTC())
}

func (s *server) executeAPISandboxRun(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun) error {
	if run.PartnerOutcomeUntil != nil {
		return s.finishSandboxPartnerWebhookOutcome(ctx, store, run)
	}
	app, err := s.store.AppByID(ctx, run.AppID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		run.Status, run.FailureCode = api.APISandboxRunFailed, closedSandboxRunFailure(*run)
		return nil
	case err != nil:
		return err
	case !app.Manifest.APISandboxRun || !state.APISandboxAcceptsRequests(app, time.Now()):
		run.Status, run.FailureCode = api.APISandboxRunFailed, closedSandboxRunFailure(*run)
		return nil
	}
	operation, err := s.store.(state.APISandboxOperationStore).GetAPISandboxOperation(ctx, app.ID)
	if err != nil {
		return err
	}
	receipt, err := s.apiSandboxResponse(ctx, app, operation)
	if err != nil {
		return err
	}
	if receipt.Status != state.APISandboxLive || operation.Scenario == nil ||
		operation.Scenario.Revision != run.ScenarioRevision || len(operation.Scenario.Requests) == 0 {
		run.Status, run.FailureCode = api.APISandboxRunFailed, "sandbox_not_ready"
		if receipt.Failure != nil {
			run.FailureCode = receipt.Failure.Code
		}
		return nil
	}
	if run.Mode == api.APISandboxRunModePartner {
		if operation.Scenario.StripeWebhook != nil || operation.Scenario.OutboundWebhook != nil || partnerScenarioValidation(operation.Scenario.Requests) != "" || len(operation.Scenario.Requests) != run.PartnerStepCount+1 {
			run.Status, run.FailureCode = api.APISandboxRunFailed, "sandbox_not_ready"
			return nil
		}
		if run.SandboxDeploymentID == "" {
			run.SandboxDeploymentID = receipt.DeploymentID
			if err := store.OpenAPISandboxPartnerRun(ctx, *run, time.Now().UTC()); err != nil {
				return err
			}
			run.Status = api.APISandboxRunAwaitingPartner
			return nil
		}
		if run.SandboxDeploymentID != receipt.DeploymentID || len(run.Steps) != run.PartnerStepCount {
			run.Status, run.FailureCode = api.APISandboxRunFailed, "sandbox_not_ready"
			return nil
		}
	}
	run.SandboxDeploymentID = receipt.DeploymentID
	if operation.Scenario.OutboundWebhook != nil {
		return s.runAPISandboxOutboundWebhook(ctx, store, run, app, operation.Scenario)
	}
	return s.runAPISandboxSteps(ctx, store, run, app, operation.Scenario, receipt)
}

func closedSandboxRunFailure(run state.APISandboxRun) string {
	if run.Mode == api.APISandboxRunModePartner && run.SandboxDeploymentID != "" && len(run.Steps) < run.PartnerStepCount {
		return "partner_requests_missing"
	}
	return "sandbox_ended"
}

func (s *server) runAPISandboxSteps(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun, app state.App, scenario *api.APISandboxScenario, sandbox api.APISandboxResponse) error {
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return err
	}
	consumer, err := s.apiSandboxConsumer(ctx, app, api.APISandboxRunConsumerRef, "API sandbox scenario runner")
	if err != nil {
		return err
	}
	expires := time.Now().Add(apiSandboxRunLease)
	if app.PreviewExpiresAt.Before(expires) {
		expires = *app.PreviewExpiresAt
	}
	scopes := []string{"read", "write"}
	if run.Mode == api.APISandboxRunModePartner || scenario.StripeWebhook != nil {
		scopes = []string{"read"}
	}
	key, token, problem := s.issueConsumerKey(ctx, acct, consumer, "run-"+uuid.NewString(), scopes, &expires)
	if problem != nil {
		run.Status, run.FailureCode = api.APISandboxRunFailed, "run_credentials_unavailable"
		return nil
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if _, err := s.store.RevokeConsumerKey(cleanup, acct.ID, key.ID); err != nil {
			s.log.WarnContext(cleanup, "revoke scenario run key", "run_id", run.ID, "err", err)
		}
	}()
	if scenario.StripeWebhook != nil {
		return s.runAPISandboxStripeWebhook(ctx, store, run, app, scenario, sandbox.URL, token)
	}
	for i, example := range scenario.Requests[run.PartnerStepCount:] {
		step := apisandbox.RunStep(ctx, s.apiSandboxProbeClient, sandbox.URL, sandbox.DeploymentID, token, run.PartnerStepCount+i+1, example)
		if run.Mode == api.APISandboxRunModePartner {
			step.Path, _, _ = strings.Cut(example.Path, "?")
		}
		if err := store.AppendAPISandboxRunStep(ctx, *run, step, time.Now().UTC()); err != nil {
			return err
		}
		if step.FailureCode != "" {
			run.Status, run.FailureCode = api.APISandboxRunFailed, step.FailureCode
			return nil
		}
	}
	run.Status = api.APISandboxRunPassed
	return nil
}
