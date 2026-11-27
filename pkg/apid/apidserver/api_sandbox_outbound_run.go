package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const sandboxOutboundWebhookTimeout = 75 * time.Second

func (s *server) runAPISandboxOutboundWebhook(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun, app state.App, scenario *api.APISandboxScenario) error {
	waitCtx, cancel := context.WithTimeout(ctx, sandboxOutboundWebhookTimeout)
	defer cancel()

	job := scenario.Requests[0]
	step := api.APISandboxRunStep{
		Index: 1, Title: job.Title, Kind: api.APISandboxStepOutboundJob, Method: job.Method,
		Path: job.Path, ExpectedStatus: http.StatusAccepted,
	}
	start := time.Now()
	acct, err := state.AppResourceAccount(waitCtx, s.store, app)
	if err != nil {
		return err
	}
	limits := api.MustLimitsFor(acct.Plan)
	hook, failure := s.createSandboxOutboundReceiver(waitCtx, app, *run, limits)
	if failure != "" {
		step.FailureCode = failure
		step.DurationMS = time.Since(start).Milliseconds()
		return checkpointAPISandboxWebhookStep(ctx, store, run, step)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if err := s.store.DeleteAppWebhook(cleanup, hook.ID); err != nil {
			s.log.WarnContext(cleanup, "remove sandbox webhook receiver", "run_id", run.ID, "err", err)
		}
	}()

	inv, problem := s.enqueueSandboxOutboundJob(waitCtx, app, *run, hook, job, limits)
	step.DurationMS = time.Since(start).Milliseconds()
	if problem != nil {
		step.FailureCode = "outbound_job_rejected"
		step.HTTPStatus = problem.Status
		return checkpointAPISandboxWebhookStep(ctx, store, run, step)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		_, _ = s.store.CancelPendingInvocation(cleanup, inv.ID)
	}()
	step.InvocationID, step.HTTPStatus, step.ObservedAt = inv.ID, http.StatusAccepted, &inv.CreatedAt
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, step); err != nil {
		return err
	}
	return s.checkSandboxOutboundDelivery(ctx, waitCtx, store, run, app, hook, inv, scenario.OutboundWebhook)
}

func (s *server) createSandboxOutboundReceiver(ctx context.Context, app state.App, run state.APISandboxRun, limits api.Limits) (state.AppWebhook, string) {
	if !limits.AsyncInvokeAllowed || limits.WebhookPerApp <= 0 {
		return state.AppWebhook{}, "outbound_webhook_unavailable"
	}
	var sealed []byte
	if run.PartnerWebhook {
		p, err := s.store.(state.APISandboxWebhookPartnerStore).GetAPISandboxWebhookPartner(ctx, run.ID)
		if err != nil || !p.ReadyFor(run.SourceDeploymentID, run.ScenarioRevision, time.Now()) {
			return state.AppWebhook{}, "partner_receiver_unavailable"
		}
		sealed = p.SigningSecretSealed
	} else {
		secret, _, err := generateInboundWebhookToken()
		if err != nil {
			return state.AppWebhook{}, "outbound_webhook_unavailable"
		}
		var problem *api.Problem
		sealed, problem = sealAppWebhookSecret(secret)
		if problem != nil {
			return state.AppWebhook{}, "outbound_webhook_unavailable"
		}
	}
	hook, err := s.store.CreateAppWebhookIfUnderQuota(ctx, state.AppWebhook{
		Scope: state.AppWebhookScopeApp, AppID: app.ID, AccountID: app.AccountID,
		TargetURL: api.APISandboxWebhookReceiverPrefix + run.ID, SecretSealed: sealed, Enabled: true,
		EventFilter: []string{string(state.AppWebhookEventJobFinished)}, DeliveryFormat: state.AppWebhookDeliveryFormatJSON,
		RetryPolicy: state.AppWebhookRetryAggressive,
	}, limits)
	if err != nil {
		return state.AppWebhook{}, "outbound_webhook_unavailable"
	}
	return hook, ""
}

func (s *server) enqueueSandboxOutboundJob(ctx context.Context, app state.App, run state.APISandboxRun, hook state.AppWebhook, job api.APISandboxExampleRequest, limits api.Limits) (state.Invocation, *api.Problem) {
	headers := map[string]string{api.RevisionHeader: run.SandboxDeploymentID}
	if len(job.Body) != 0 {
		headers["Content-Type"] = "application/json"
	}
	body, _ := json.Marshal(headers)
	deadline, _ := ctx.Deadline()
	inv, problem := s.enqueuePreparedInvocation(ctx, state.Invocation{
		ID: run.ID, AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationAsyncInvoke,
		Method: job.Method, Path: job.Path, Payload: job.Body, Headers: body,
		DueAt: time.Now().UTC(), DeadlineAt: &deadline, OnSuccessDestinationID: hook.ID,
		RetryPolicyJSON:     effectiveInvocationRetryPolicy(app, nil, limits.MaxQueueAttempts),
		AdmissionMaxPending: limits.MaxQueueDepth, AdmissionMaxBacklogBytes: limits.MaxQueueBacklogBytesPerAccount,
	}, "enqueue sandbox webhook job")
	if problem == nil {
		message, _ := json.Marshal(map[string]string{
			"invocation_id": inv.ID, "app_id": inv.AppID, "source": string(inv.Source),
		})
		_ = s.notif.Notify(ctx, db.NotifyInvocationDue, string(message))
	}
	return inv, problem
}

func (s *server) checkSandboxOutboundDelivery(ctx, waitCtx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun, app state.App, hook state.AppWebhook, inv state.Invocation, check *api.APISandboxOutboundWebhook) error {
	delivery, step := s.waitForSandboxOutboundQueue(waitCtx, hook, inv)
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, step); err != nil || step.FailureCode != "" {
		return err
	}

	attempts := []int{http.StatusNoContent}
	if run.PartnerWebhook {
		attempts = []int{0, 0} // Any 2xx is a valid partner acknowledgement.
	} else if check.RetryOnce {
		attempts = []int{http.StatusServiceUnavailable, http.StatusNoContent}
	}
	for index, expected := range attempts {
		step = s.waitForSandboxOutboundAttempt(waitCtx, hook, delivery.ID, index+1, expected, run.PartnerWebhook)
		if err := checkpointAPISandboxWebhookStep(ctx, store, run, step); err != nil || step.FailureCode != "" {
			return err
		}
	}
	if len(check.Assertions) != 0 {
		var payload struct {
			Result json.RawMessage `json:"result"`
		}
		step = api.APISandboxRunStep{
			Index: len(attempts) + 3, Title: "Check callback result", Kind: api.APISandboxStepOutboundPayload,
			Method: http.MethodPost, EventType: string(state.AppWebhookEventJobFinished), DeliveryID: delivery.ID,
		}
		if json.Unmarshal(delivery.Payload, &payload) != nil ||
			apisandbox.CheckJSONAssertions(payload.Result, check.Assertions, &step) != "" {
			step.FailureCode = "outbound_webhook_payload_mismatch"
		}
		if err := checkpointAPISandboxWebhookStep(ctx, store, run, step); err != nil || step.FailureCode != "" {
			return err
		}
	}
	if run.PartnerWebhook {
		return s.parkSandboxPartnerWebhookOutcome(ctx, run, app)
	}
	run.Status = api.APISandboxRunPassed
	return nil
}

func (s *server) waitForSandboxOutboundQueue(ctx context.Context, hook state.AppWebhook, inv state.Invocation) (state.AppWebhookDelivery, api.APISandboxRunStep) {
	start := time.Now()
	step := api.APISandboxRunStep{
		Index: 2, Title: "Queue completion webhook", Kind: api.APISandboxStepOutboundQueued,
		Method: http.MethodPost, EventType: string(state.AppWebhookEventJobFinished), InvocationID: inv.ID,
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		current, err := s.store.InvocationByID(ctx, inv.ID)
		if err != nil {
			step.FailureCode = "outbound_job_unavailable"
			break
		}
		step.InvocationState = string(current.State)
		switch current.State {
		case state.InvocationCompleted:
			rows, _, err := s.store.ListAppWebhookDeliveries(ctx, hook.AppID, hook.ID, 2, "")
			if err == nil && len(rows) == 1 && rows[0].Event == state.AppWebhookEventJobFinished {
				var payload struct {
					JobID string `json:"job_id"`
				}
				if json.Unmarshal(rows[0].Payload, &payload) == nil && payload.JobID == inv.ID {
					step.DeliveryID, step.ObservedAt = rows[0].ID, &rows[0].CreatedAt
					step.DurationMS = time.Since(start).Milliseconds()
					return rows[0], step
				}
			}
			step.FailureCode = "outbound_webhook_not_queued"
		case state.InvocationPending, state.InvocationDispatching:
		default:
			step.FailureCode = "outbound_job_failed"
		}
		if step.FailureCode != "" {
			break
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	if ctx.Err() != nil {
		step.FailureCode = "outbound_job_timeout"
	}
	step.DurationMS = time.Since(start).Milliseconds()
	return state.AppWebhookDelivery{}, step
}

func (s *server) waitForSandboxOutboundAttempt(ctx context.Context, hook state.AppWebhook, deliveryID string, number, expectedStatus int, partner bool) api.APISandboxRunStep {
	start := time.Now()
	step := api.APISandboxRunStep{
		Index: number + 2, Title: "Deliver completion webhook", Kind: api.APISandboxStepOutboundAttempt,
		Method: http.MethodPost, EventType: string(state.AppWebhookEventJobFinished), DeliveryID: deliveryID,
		AttemptNumber: number, ExpectedStatus: expectedStatus,
	}
	expectedOutcome := "succeeded"
	if partner && number == 1 {
		expectedOutcome, step.Title = "retrying", "Simulate lost callback acknowledgement"
	} else if expectedStatus == http.StatusServiceUnavailable {
		expectedOutcome = "retrying"
		step.Title = "Verify callback will retry"
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		history, _, err := s.store.ListAppWebhookDeliveryAttempts(ctx, deliveryID, hook.ID, hook.AccountID, 4, "")
		if err != nil {
			step.FailureCode = "outbound_webhook_delivery_unavailable"
			break
		}
		i := slices.IndexFunc(history, func(attempt state.AppWebhookDeliveryAttempt) bool {
			return attempt.ReplayGeneration == 0 && attempt.AttemptNumber == number
		})
		if i >= 0 {
			attempt := history[i]
			step.HTTPStatus, step.ObservedAt = attempt.ResponseCode, &attempt.FinishedAt
			step.DurationMS = max(0, attempt.FinishedAt.Sub(attempt.StartedAt).Milliseconds())
			statusOK := attempt.ResponseCode == expectedStatus
			if partner {
				statusOK = attempt.ResponseCode >= 200 && attempt.ResponseCode < 300
				if number == 1 {
					step.AcknowledgementDropped = statusOK && attempt.Outcome == "retrying" &&
						attempt.Error == "webhook: "+api.APISandboxLostAcknowledgement
					statusOK = step.AcknowledgementDropped
				}
			}
			if !statusOK || attempt.Outcome != expectedOutcome {
				step.FailureCode = "outbound_webhook_unexpected_attempt"
			}
			return step
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	if ctx.Err() != nil {
		step.FailureCode = "outbound_webhook_delivery_timeout"
	}
	step.DurationMS = time.Since(start).Milliseconds()
	return step
}

func (s *server) parkSandboxPartnerWebhookOutcome(ctx context.Context, run *state.APISandboxRun, app state.App) error {
	partners := s.store.(state.APISandboxWebhookPartnerStore)
	p, err := partners.GetAPISandboxWebhookPartner(ctx, run.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	until := now.Add(5 * time.Minute)
	if p.ExpiresAt.Before(until) {
		until = p.ExpiresAt
	}
	if app.PreviewExpiresAt != nil && app.PreviewExpiresAt.Before(until) {
		until = *app.PreviewExpiresAt
	}
	if err := partners.ParkAPISandboxWebhookPartnerRun(ctx, *run, now, until); err != nil {
		return err
	}
	run.Status = api.APISandboxRunAwaitingOutcome
	return nil
}

func (s *server) finishSandboxPartnerWebhookOutcome(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun) error {
	// A worker can stop after recording the outcome and before closing the
	// sandbox. This phase can resume without sending another callback.
	if n := len(run.Steps); n > 0 && run.Steps[n-1].Kind == api.APISandboxStepPartnerOutcome {
		run.Status, run.FailureCode = api.APISandboxRunPassed, run.Steps[n-1].FailureCode
		if run.FailureCode != "" {
			run.Status = api.APISandboxRunFailed
		}
		return nil
	}
	partner, err := s.store.(state.APISandboxWebhookPartnerStore).GetAPISandboxWebhookPartner(ctx, run.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	step := api.APISandboxRunStep{
		Index: len(run.Steps) + 1, Title: "Check partner-reported outcome", Kind: api.APISandboxStepPartnerOutcome,
		Method: http.MethodPost, DeliveryID: sandboxPartnerDeliveryID(*run), ObservedAt: partner.OutcomeAt,
	}
	switch {
	case partner.OutcomeAt == nil:
		step.FailureCode = "partner_outcome_missing"
	case partner.OutcomeCount != 1:
		step.FailureCode = "partner_outcome_mismatch"
	}
	if partner.OutcomeAt != nil {
		step.PartnerAppliedCount = &partner.OutcomeCount
	}
	run.Status = api.APISandboxRunPassed
	return checkpointAPISandboxWebhookStep(ctx, store, run, step)
}
