// adr: 829
package alerts_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestAutomationFailureNotificationLifecycle(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricWorkflowFailures, state.AlertGte, api.WorkflowFailureAlertThreshold)
	create := func(status string, cancel bool) {
		t.Helper()
		run := &state.WorkflowRun{AppID: rule.AppID, WorkflowName: "invoice", Input: []byte(`{"token":"private-workflow-input"}`), DefinitionSnapshot: []byte(`{"name":"invoice","steps":[{"name":"timer","wait_for_duration":"1h"}]}`)}
		if err := store.CreateWorkflowRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		if cancel {
			if _, err := store.CancelWorkflowRun(t.Context(), run.ID, "private-cancellation-reason"); err != nil {
				t.Fatal(err)
			}
		} else if err := store.MarkWorkflowRunStatus(t.Context(), run.ID, status, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	tick := func(want int) {
		t.Helper()
		stats, err := evaluator.RunOnce(t.Context())
		if err != nil || stats.Fired != want || stats.Delivered != want {
			t.Fatalf("stats=%+v err=%v", stats, err)
		}
	}
	create(state.WorkflowRunStatusRunning, false)
	create(state.WorkflowRunStatusSucceeded, false)
	create("", true)
	now = time.Now().UTC().Add(time.Second)
	tick(0)
	create(state.WorkflowRunStatusFailed, false)
	create(state.WorkflowRunStatusDead, false)
	now = time.Now().UTC().Add(time.Second)
	tick(1)
	tick(0) // Same failures must not enqueue a duplicate notification.
	now = now.Add(6 * time.Minute)
	tick(0) // Terminal failures leave the five-minute observation window.
	stored, err := store.AlertRuleByID(t.Context(), rule.ID)
	if err != nil || stored.State != state.AlertStateOk {
		t.Fatalf("cleared rule=%+v err=%v", stored, err)
	}
	deliveries, err := store.ListAlertDeliveriesForRule(t.Context(), rule.ID, 10, false)
	if err != nil || len(deliveries) != 1 || dispatch.callCount() != 1 {
		t.Fatalf("deliveries=%d calls=%d err=%v", len(deliveries), dispatch.callCount(), err)
	}
	var payload map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["observed"] != float64(2) || payload["workflow_runs_path"] != "/v1/apps/alert-app/workflows/runs?status=failed" || payload["workflow_dead_runs_path"] != "/v1/apps/alert-app/workflows/runs?status=dead" {
		t.Fatalf("unhelpful notification: %+v", payload)
	}
	if !reflect.DeepEqual(payload, dispatch.calls[0].Payload) {
		t.Fatal("stored evidence differs from dispatched notification")
	}
	for _, secret := range []string{"private-workflow-input", "private-cancellation-reason", "super-secret-shared-key"} {
		if strings.Contains(string(deliveries[0].Payload), secret) {
			t.Fatalf("notification disclosed %s", secret)
		}
	}
}
