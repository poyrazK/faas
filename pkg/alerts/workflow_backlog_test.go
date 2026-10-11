// adr: 643
package alerts_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestEvaluatorAutomationBacklogThresholdCooldownAndRecovery(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricWorkflowDueAge, state.AlertGte, api.WorkflowBacklogAlertThresholdSeconds)
	run := &state.WorkflowRun{AppID: rule.AppID, WorkflowName: "nightly", Input: json.RawMessage(`{"private":"backlog-input-secret"}`), DefinitionSnapshot: json.RawMessage(`{"name":"nightly","steps":[{"name":"pause","wait_for_duration":"1h"}]}`)}
	if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, state.WorkflowRunStatusRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(t.Context(), run.ID, []*state.WorkflowStep{{StepName: "pause", Status: state.WorkflowStepStatusPending}}); err != nil {
		t.Fatal(err)
	}
	wake, err := store.ParkWorkflowTimer(t.Context(), run.ID, "pause", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	now := wake.Add(-time.Second)
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(store, discardLog(), nil, "meterd"),
		Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	tick := func(at time.Time, fires int) {
		t.Helper()
		now = at
		stats, err := evaluator.RunOnce(t.Context())
		if err != nil || stats.Fired != fires || stats.Delivered != fires {
			t.Fatalf("tick at %s: %+v %v", at, stats, err)
		}
	}
	tick(wake.Add(-time.Second), 0)
	tick(wake.Add(299*time.Second), 0)
	firstFire := wake.Add(300 * time.Second)
	tick(firstFire, 1)
	tick(firstFire.Add(time.Second), 0)
	cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
	tick(firstFire.Add(cooldown+time.Second), 1)
	if _, err := store.CancelWorkflowRun(t.Context(), run.ID, "resolved"); err != nil {
		t.Fatal(err)
	}
	tick(firstFire.Add(cooldown+2*time.Second), 0)
	stored, err := store.AlertRuleByID(t.Context(), rule.ID)
	if err != nil || stored.State != state.AlertStateOk {
		t.Fatalf("resolved rule=%+v %v", stored, err)
	}
	newWake := firstFire.Add(2 * cooldown)
	newRun := &state.WorkflowRun{AppID: rule.AppID, WorkflowName: "nightly", ScheduledFor: newWake, DefinitionSnapshot: run.DefinitionSnapshot}
	if err := store.CreateWorkflowRun(t.Context(), newRun); err != nil {
		t.Fatal(err)
	}
	tick(newWake.Add(300*time.Second), 1)
	deliveries, err := store.ListAlertDeliveriesForRule(t.Context(), rule.ID, 10, false)
	if err != nil || len(deliveries) != 3 || dispatch.callCount() != 3 {
		t.Fatalf("deliveries=%d calls=%d err=%v", len(deliveries), dispatch.callCount(), err)
	}
	for _, delivery := range deliveries {
		var payload map[string]any
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil || payload["metric"] != string(state.AlertMetricWorkflowDueAge) || payload["observed"].(float64) < 300 || strings.Contains(string(delivery.Payload), "backlog-input-secret") {
			t.Fatalf("backlog delivery payload=%s err=%v", delivery.Payload, err)
		}
	}
}
