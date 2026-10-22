// adr: 20261022170348
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioCapturedRunStartsAndCancellations(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, account, app := *f.Base(), f.Account(), f.App()
			platform := base.(state.ScenarioPlatformCaptureStore)
			reads := base.(state.ScenarioCapturedReadStore)
			capture := func(call state.ScenarioPlatformCallCapture) (state.ScenarioPlatformCallResult, error) {
				t.Helper()
				result, captured, err := platform.CaptureScenarioPlatformCall(ctx, app.ID, call)
				if !captured {
					t.Fatalf("%s was not captured", call.Kind)
				}
				return result, err
			}
			start := func(runtime, key, source string) state.ScenarioPlatformCallResult {
				t.Helper()
				result, err := capture(state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindExecution, Name: runtime, IdempotencyKey: key,
					Message: json.RawMessage(`{"execution":{"runtime":"` + runtime + `","source_sha256":"` + source + `"}}`)})
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			first := start("python312", "k1", "a")
			// apid replays the first response under a key whatever the body.
			if again := start("node22", "k1", "b"); again.ID != first.ID || !again.Replayed {
				t.Fatalf("keyed start replay = %+v, want %s", again, first.ID)
			}
			second := start("python312", "", "c")
			cancel := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindExecutionCancel, Address: second.ID, Name: "cancel", Message: json.RawMessage(`{}`)}
			if _, err := capture(cancel); err != nil {
				t.Fatal(err)
			}
			workflow, err := capture(state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: app.Slug, Name: "invoice", Message: json.RawMessage(`{"input":{}}`)})
			if err != nil {
				t.Fatal(err)
			}
			for _, address := range []string{uuid.NewString(), "not-a-uuid", workflow.ID} {
				refused := cancel
				refused.Address = address
				if _, err := capture(refused); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("cancel of %q outside the run's Run starts: %v", address, err)
				}
			}

			if got, err := reads.ScenarioCapturedPlatformCall(ctx, account.ID, state.ActionCaptureTestRun, first.ID); err != nil || got.Kind != api.ScenarioPlatformKindExecution ||
				got.Name != "python312" || got.CancelledAt != nil || got.CreatedAt.IsZero() {
				t.Fatalf("queued Run read = %+v %v", got, err)
			}
			got, err := reads.ScenarioCapturedPlatformCall(ctx, account.ID, state.ActionCaptureTestRun, second.ID)
			if err != nil || got.CancelledAt == nil || got.CancelledAt.Before(got.CreatedAt) {
				t.Fatalf("cancelled Run read = %+v %v", got, err)
			}
			if got, err := reads.ScenarioCapturedPlatformCall(ctx, account.ID, state.ActionCaptureTestRun, workflow.ID); err != nil || got.CancelledAt != nil {
				t.Fatalf("workflow run read = %+v %v", got, err)
			}

			listed, err := reads.ScenarioCapturedExecutions(ctx, account.ID, state.ActionCaptureTestRun)
			if err != nil || len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID ||
				listed[0].Record.CancelledAt == nil || listed[1].Record.CancelledAt != nil || listed[1].Record.Name != "python312" {
				t.Fatalf("captured Run list = %+v %v", listed, err)
			}
			if other, err := reads.ScenarioCapturedExecutions(ctx, account.ID, strings.Repeat("c", 32)); err != nil || len(other) != 0 {
				t.Fatalf("another run's Run list = %+v %v", other, err)
			}

			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" {
				t.Fatalf("sealed evidence = %+v %v", snapshot, err)
			}
			var targets []string
			for _, call := range snapshot.PlatformCalls {
				if call.Workload != "primary" {
					t.Fatalf("platform call workload = %+v", call)
				}
				targets = append(targets, call.Kind+" "+call.Target+" "+call.Name)
			}
			want := []string{"execution execution:runs python312", "execution execution:runs python312",
				"execution_cancel execution:primary/python312#2 cancel", "workflow_run workload:primary invoice"}
			if strings.Join(targets, "|") != strings.Join(want, "|") {
				t.Fatalf("sealed platform calls = %q, want %q", targets, want)
			}
		})
	}
}

func TestRehearsalNetworkPolicyCarriesTheAccountPlan(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend)
			expires := time.Now().Add(time.Hour)
			app, err := (*f.Base()).CreateApp(t.Context(), state.App{AccountID: (*f.Account()).ID, Slug: "network-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128,
				PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			member := state.ScenarioTestMember{AppID: app.ID, Workload: "primary", WebhookMode: state.ScenarioWebhookCaptureMode, NetworkMode: state.ScenarioNetworkContained}
			if err := (*f.Base()).RegisterScenarioTestMembers(t.Context(), (*f.Account()).ID, state.ActionCaptureTestRun, []state.ScenarioTestMember{member}); err != nil {
				t.Fatal(err)
			}
			policy, ok, err := (*f.Base()).(state.ScenarioRehearsalNetworkPolicyStore).ScenarioRehearsalNetworkPolicy(t.Context(), app.ID)
			if err != nil || !ok || policy.RunID != state.ActionCaptureTestRun || policy.AccountID != (*f.Account()).ID || policy.Plan != api.PlanPro {
				t.Fatalf("policy = %+v %v %v", policy, ok, err)
			}
		})
	}
}
