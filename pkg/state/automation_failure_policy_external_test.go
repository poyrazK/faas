// adr: 905
package state_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func automationFailureStores(t *testing.T, test func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		test(t, state.NewPgStore(pool))
	})
}
func automationFailureFixture(t *testing.T, store state.Store) (state.App, state.Deployment) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "failure-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`[{"name":"nightly","trigger":{"type":"schedule","schedule":"* * * * *","overlap":"allow"},"steps":[{"name":"main","path":"/report"}]},{"name":"paid","trigger":{"type":"event","source":"billing.stripe","event_type":"invoice.paid"},"steps":[{"name":"main","path":"/invoice"}]},{"name":"hooked","trigger":{"type":"manual"},"steps":[{"name":"main","path":"/hook"}]}]`)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending, Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return app, dep
}
func automationFailureRun(t *testing.T, store state.Store, appID, name, status string) *state.WorkflowRun {
	t.Helper()
	run := &state.WorkflowRun{AppID: appID, WorkflowName: name, Input: json.RawMessage(`{"secret":"private-input"}`), DefinitionSnapshot: json.RawMessage(`{"name":"` + name + `","steps":[{"name":"main","path":"/report"}]}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		private := "private-executor-error"
		if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, status, nil, &private); err != nil {
			t.Fatal(err)
		}
	}
	return run
}
func TestAutomationFailurePolicyCountsLatchResumeAndPrivacy(t *testing.T) {
	automationFailureStores(t, func(t *testing.T, store state.Store) {
		app, _ := automationFailureFixture(t, store)
		policies := store.(state.AutomationFailurePolicyStore)
		ctx := t.Context()
		enabled := true
		req := api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 3, MinCompletedRuns: 5, WindowSeconds: 300}
		out, err := policies.SetAutomationFailurePolicy(ctx, app.ID, "nightly", req)
		if err != nil || out.Policy.Version != 1 || out.Paused {
			t.Fatalf("configure=%+v %v", out, err)
		}
		if _, err = policies.SetAutomationFailurePolicy(ctx, app.ID, "nightly", req); !errors.Is(err, state.ErrAutomationFailurePolicyConflict) {
			t.Fatalf("CAS bypass: %v", err)
		}
		hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: app.AccountID, TargetURL: "https://example.com/hooks", SecretSealed: []byte("private-hook-secret"), EventFilter: []string{"automation.paused"}, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{state.WorkflowRunStatusFailed, state.WorkflowRunStatusDead, state.WorkflowRunStatusFailed} {
			automationFailureRun(t, store, app.ID, "nightly", status)
		}
		canceled := automationFailureRun(t, store, app.ID, "nightly", "")
		if _, err = store.CancelWorkflowRun(ctx, canceled.ID, "private-cancel-reason"); err != nil {
			t.Fatal(err)
		}
		evaluate := func() {
			t.Helper()
			if _, err := policies.EvaluateAutomationFailurePolicies(ctx, "", "", api.AutomationFailurePolicyBatch, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
		evaluate()
		out, err = policies.GetAutomationFailurePolicy(ctx, app.ID, "nightly")
		if err != nil || out.Paused || out.ObservedCompletedRuns != 3 || out.ObservedFailures != 3 {
			t.Fatalf("sample gate=%+v %v", out, err)
		}
		active := automationFailureRun(t, store, app.ID, "nightly", "")
		for i := 0; i < 2; i++ {
			automationFailureRun(t, store, app.ID, "nightly", state.WorkflowRunStatusSucceeded)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := policies.EvaluateAutomationFailurePolicies(ctx, "", "", api.AutomationFailurePolicyBatch, time.Now().UTC())
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		out, err = policies.GetAutomationFailurePolicy(ctx, app.ID, "nightly")
		if err != nil || !out.Paused || out.Generation != 1 || len(out.History) != 1 || out.PendingRuns != 1 {
			t.Fatalf("pause=%+v %v", out, err)
		}
		if run, err := store.GetWorkflowRun(ctx, active.ID); err != nil || run.Status != state.WorkflowRunStatusPending {
			t.Fatalf("existing run changed=%+v %v", run, err)
		}
		if _, err = store.(state.AppWebhookEventOutboxStore).DrainAppWebhookEventOutbox(ctx, 100); err != nil {
			t.Fatal(err)
		}
		deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 100, "")
		if err != nil || len(deliveries) != 1 || deliveries[0].Event != state.AppWebhookEventAutomationPaused {
			t.Fatalf("duplicate/missing pause notification: %d %v", len(deliveries), err)
		}
		for _, private := range []string{"private-input", "private-executor-error", "private-hook-secret", "private-cancel-reason"} {
			if strings.Contains(string(deliveries[0].Payload), private) {
				t.Fatalf("notification leaked %s", private)
			}
		}
		enabled = false
		req.ExpectedVersion = 1
		out, err = policies.SetAutomationFailurePolicy(ctx, app.ID, "nightly", req)
		if err != nil || !out.Paused {
			t.Fatalf("disabling bypassed latch=%+v %v", out, err)
		}
		if _, err = policies.ResumeAutomationFailurePause(ctx, app.ID, "nightly", uuid.NewString(), api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("wrong actor resumed: %v", err)
		}
		if _, err = policies.ResumeAutomationFailurePause(ctx, app.ID, "nightly", app.AccountID, api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 2}); !errors.Is(err, state.ErrAutomationFailurePolicyConflict) {
			t.Fatalf("stale generation: %v", err)
		}
		out, err = policies.ResumeAutomationFailurePause(ctx, app.ID, "nightly", app.AccountID, api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1})
		if err != nil || out.Paused || out.Generation != 2 || out.ObservedFailures != 0 || len(out.History) != 2 || out.History[0].ActorAccountID != app.AccountID {
			t.Fatalf("resume=%+v %v", out, err)
		}
		if _, err = policies.ResumeAutomationFailurePause(ctx, app.ID, "nightly", app.AccountID, api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1}); !errors.Is(err, state.ErrAutomationFailurePolicyConflict) {
			t.Fatalf("replay resume: %v", err)
		}
		enabled = true
		req.ExpectedVersion = 2
		req.FailureThreshold = 1
		req.MinCompletedRuns = 1
		if _, err = policies.SetAutomationFailurePolicy(ctx, app.ID, "nightly", req); err != nil {
			t.Fatal(err)
		}
		evaluate()
		out, err = policies.GetAutomationFailurePolicy(ctx, app.ID, "nightly")
		if err != nil || out.Paused {
			t.Fatalf("old failures retripped=%+v %v", out, err)
		}
		automationFailureRun(t, store, app.ID, "nightly", state.WorkflowRunStatusFailed)
		evaluate()
		out, err = policies.GetAutomationFailurePolicy(ctx, app.ID, "nightly")
		if err != nil || !out.Paused || out.Generation != 3 || len(out.History) != 3 {
			t.Fatalf("new failures failed to trip=%+v %v", out, err)
		}
	})
}
func TestAutomationFailurePauseBlocksAllAutomaticAdmissionPaths(t *testing.T) {
	automationFailureStores(t, func(t *testing.T, store state.Store) {
		app, dep := automationFailureFixture(t, store)
		ctx := t.Context()
		policies := store.(state.AutomationFailurePolicyStore)
		enabled := true
		for _, name := range []string{"nightly", "paid", "hooked"} {
			if _, err := policies.SetAutomationFailurePolicy(ctx, app.ID, name, api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 300}); err != nil {
				t.Fatal(err)
			}
		}
		payload, _ := json.Marshal(map[string]any{"specversion": "1.0", "id": uuid.NewString(), "source": "billing.stripe", "type": "invoice.paid", "accountid": app.AccountID, "datacontenttype": "application/json", "time": time.Now().UTC(), "data": map[string]any{"secret": "private-event"}})
		if err := store.AppendEvent(ctx, "apid", "event.published", &app.AccountID, payload); err != nil {
			t.Fatal(err)
		}
		work, err := store.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil || len(work.RecipientSnapshot) != 1 {
			t.Fatalf("capture=%+v %v", work, err)
		}
		endpoint, err := store.(state.InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{AppID: app.ID, AccountID: app.AccountID, Name: "guard", Provider: state.InboundWebhookProviderStripe, DeliveryPath: "/", TokenHash: bytes.Repeat([]byte{1}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true}, api.MustLimitsFor(api.PlanHobby))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.(state.WebhookAutomationStore).SaveWebhookAutomationBinding(ctx, state.WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "hooked", EventType: "invoice.*"}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if _, _, err = store.(state.WorkflowScheduleStore).AdmitScheduledWorkflow(ctx, app.ID, dep.ID, "nightly", now); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"nightly", "paid", "hooked"} {
			automationFailureRun(t, store, app.ID, name, state.WorkflowRunStatusFailed)
		}
		if _, err = policies.EvaluateAutomationFailurePolicies(ctx, "", "", 100, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := store.(state.WorkflowScheduleStore).AdmitScheduledWorkflow(ctx, app.ID, dep.ID, "nightly", now.Add(time.Minute)); err != nil || changed {
			t.Fatalf("paused schedule admitted: %t %v", changed, err)
		}
		if _, err = store.(state.EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); !errors.Is(err, state.ErrWorkflowEventTargetUnavailable) {
			t.Fatalf("captured event admitted during pause: %v", err)
		}
		out, err := policies.GetAutomationFailurePolicy(ctx, app.ID, "paid")
		if err != nil || out.RetainedEvents != 1 {
			t.Fatalf("resume preview=%+v %v", out, err)
		}
		candidates, err := store.(state.EventWorkflowStore).ListMatchingEventWorkflows(ctx, app.AccountID, "billing.stripe", "invoice.paid", "", 100)
		if err != nil || len(candidates) != 0 {
			t.Fatalf("paused event matched=%+v %v", candidates, err)
		}
		receipt, _, err := store.(state.WebhookAutomationStore).AcceptWebhookAutomation(ctx, endpoint, json.RawMessage(`{"id":"evt_guard","type":"invoice.paid","data":{"secret":"private"}}`), true)
		if err != nil || receipt.IgnoredReason != "automation_failure_paused" {
			t.Fatalf("paused webhook=%+v %v", receipt, err)
		}
		// Manual runs remain available for investigation.
		automationFailureRun(t, store, app.ID, "paid", state.WorkflowRunStatusSucceeded)
		if _, err = policies.ResumeAutomationFailurePause(ctx, app.ID, "paid", app.AccountID, api.ResumeAutomationFailurePauseRequest{ExpectedGeneration: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.(state.EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil {
			t.Fatalf("retained event did not resume: %v", err)
		}
	})
}
func TestAutomationFailurePolicyValidationAndPublishedTarget(t *testing.T) {
	store := state.NewMemStore()
	app, _ := automationFailureFixture(t, store)
	enabled := true
	valid := api.SetAutomationFailurePolicyRequest{Enabled: &enabled, FailureThreshold: 1, MinCompletedRuns: 1, WindowSeconds: 60}
	for _, change := range []func(*api.SetAutomationFailurePolicyRequest){func(r *api.SetAutomationFailurePolicyRequest) { r.Enabled = nil }, func(r *api.SetAutomationFailurePolicyRequest) { r.FailureThreshold = 0 }, func(r *api.SetAutomationFailurePolicyRequest) { r.MinCompletedRuns = 10001 }, func(r *api.SetAutomationFailurePolicyRequest) { r.WindowSeconds = 59 }, func(r *api.SetAutomationFailurePolicyRequest) { r.ExpectedVersion = -1 }} {
		r := valid
		change(&r)
		if _, err := store.SetAutomationFailurePolicy(context.Background(), app.ID, "nightly", r); !errors.Is(err, state.ErrAutomationInvalid) {
			t.Fatalf("invalid policy accepted: %+v %v", r, err)
		}
	}
	if _, err := store.SetAutomationFailurePolicy(t.Context(), app.ID, "missing", valid); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unpublished policy accepted: %v", err)
	}
}
