package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestLoginTargetAlertSendsObservationLinkWithoutDeploymentAction(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricPreAuthTargetThreshold, state.AlertGt, 5)
	window, action := state.AlertWindow15m, "rollback"
	if _, err := store.UpdateAlertRule(context.Background(), rule.ID, state.UpdateAlertRuleParams{
		WindowSpec: &window, Action: &action,
	}); err != nil {
		t.Fatalf("seed invalid stored action: %v", err)
	}
	queries := 0
	prom := &selectivePromQL{fn: func(query string) (float64, error) {
		queries++
		for _, part := range []string{`gateway_pre_auth_policy_shadow_total`, `outcome="target_threshold"`, `[15m]`} {
			if !strings.Contains(query, part) {
				t.Errorf("query %q missing %q", query, part)
			}
		}
		return 8, nil
	}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	actions := &recordingActionExecutor{}
	ev, _ := makeEvaluatorWithAction(t, store, prom, ident, dispatch, actions)
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if queries != 1 || stats.Fired != 1 || stats.Delivered != 1 || stats.ActionSkipped != 1 {
		t.Fatalf("queries=%d stats=%+v, want one query and webhook-only fire", queries, stats)
	}
	if actions.calls != 0 || dispatch.callCount() != 1 {
		t.Fatalf("deployment actions=%d webhook calls=%d, want 0 and 1", actions.calls, dispatch.callCount())
	}
	got := dispatch.calls[0].Payload
	if got["observations_path"] != "/v1/apps/alert-app/pre-auth-observations?range=15m" || got["metric"] != "pre_auth_target_threshold" {
		t.Fatalf("alert payload=%v, missing observations path or metric", got)
	}
	if got["dashboard_path"] != "/dashboard/apps/alert-app/pre-auth?range=15m" {
		t.Fatalf("alert payload=%v, missing dashboard path", got)
	}
	for key := range got {
		if strings.Contains(key, "target_digest") || strings.Contains(key, "login_identifier") {
			t.Fatalf("payload includes sensitive key %q", key)
		}
	}
}

func TestLoginTargetAlertSkipsDegradedSource(t *testing.T) {
	store := state.NewMemStore()
	_, ident, _ := seedRule(t, store, state.AlertMetricPreAuthTargetThreshold, state.AlertGt, 5)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, &stubPromQL{err: errors.New("prometheus unavailable")}, ident, dispatch)
	stats, err := ev.RunOnce(context.Background())
	if err != nil || stats.Fired != 0 || stats.SkippedDegraded != 1 || dispatch.callCount() != 0 {
		t.Fatalf("stats=%+v err=%v calls=%d, want degraded without fire", stats, err, dispatch.callCount())
	}
}

func TestLoginTargetSignalHealthKeepsLowSampleUnknownAndAlertsOnCoverageGap(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricPreAuthTargetSignalGapPct, state.AlertGt, 10)
	window := state.AlertWindow15m
	if _, err := store.UpdateAlertRule(context.Background(), rule.ID, state.UpdateAlertRuleParams{WindowSpec: &window}); err != nil {
		t.Fatalf("set alert window: %v", err)
	}
	observed := -1.0
	prom := &selectivePromQL{fn: func(query string) (float64, error) {
		for _, part := range []string{`outcome=~"target_missing|target_invalid"`, `outcome=~"target_failure|target_missing|target_invalid"`, `>= 20`, `[15m]`} {
			if !strings.Contains(query, part) {
				t.Errorf("query %q missing %q", query, part)
			}
		}
		return observed, nil
	}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, prom, ident, dispatch)

	stats, err := ev.RunOnce(context.Background())
	if err != nil || stats.SkippedInsufficient != 1 || stats.Fired != 0 || dispatch.callCount() != 0 {
		t.Fatalf("low-sample stats=%+v err=%v calls=%d", stats, err, dispatch.callCount())
	}
	got, _ := store.AlertRuleByID(context.Background(), rule.ID)
	if got.State != state.AlertStateUnknown || got.LastEvaluatedAt.IsZero() {
		t.Fatalf("low-sample rule state=%q last_evaluated=%v, want unknown and stamped", got.State, got.LastEvaluatedAt)
	}

	observed = 0
	stats, err = ev.RunOnce(context.Background())
	got, _ = store.AlertRuleByID(context.Background(), rule.ID)
	if err != nil || stats.Fired != 0 || got.State != state.AlertStateOk {
		t.Fatalf("valid coverage stats=%+v state=%q err=%v, want ok", stats, got.State, err)
	}

	observed = 25
	stats, err = ev.RunOnce(context.Background())
	got, _ = store.AlertRuleByID(context.Background(), rule.ID)
	if err != nil || stats.Fired != 1 || dispatch.callCount() != 1 || got.State != state.AlertStateFiring {
		t.Fatalf("coverage gap stats=%+v state=%q calls=%d err=%v", stats, got.State, dispatch.callCount(), err)
	}
	payload := dispatch.calls[0].Payload
	if payload["observations_path"] != "/v1/apps/alert-app/pre-auth-observations?range=15m" ||
		payload["dashboard_path"] != "/dashboard/apps/alert-app/pre-auth?range=15m" ||
		payload["observed"] != float64(25) {
		t.Fatalf("coverage payload=%v", payload)
	}
	if _, ok := payload["target_digest"]; ok {
		t.Fatalf("coverage payload exposes target digest: %v", payload)
	}
}

func TestLoginTargetSignalHealthSkipsTelemetryOutage(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricPreAuthTargetSignalGapPct, state.AlertGt, 10)
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, &stubPromQL{err: errors.New("prometheus unavailable")}, ident, dispatch)
	stats, err := ev.RunOnce(context.Background())
	got, _ := store.AlertRuleByID(context.Background(), rule.ID)
	if err != nil || stats.SkippedDegraded != 1 || stats.Fired != 0 || dispatch.callCount() != 0 || got.State != state.AlertStateDegraded {
		t.Fatalf("telemetry outage stats=%+v state=%q calls=%d err=%v", stats, got.State, dispatch.callCount(), err)
	}
}

func TestLoginTargetSignalHealthNeverRunsDeploymentAction(t *testing.T) {
	store := state.NewMemStore()
	rule, ident, _ := seedRule(t, store, state.AlertMetricPreAuthTargetSignalGapPct, state.AlertGt, 10)
	action := "rollback"
	if _, err := store.UpdateAlertRule(context.Background(), rule.ID, state.UpdateAlertRuleParams{Action: &action}); err != nil {
		t.Fatalf("seed invalid stored action: %v", err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	actions := &recordingActionExecutor{}
	prom := &selectivePromQL{fn: func(string) (float64, error) { return 25, nil }}
	ev, _ := makeEvaluatorWithAction(t, store, prom, ident, dispatch, actions)
	stats, err := ev.RunOnce(context.Background())
	if err != nil || stats.Fired != 1 || stats.Delivered != 1 || stats.ActionSkipped != 1 ||
		dispatch.callCount() != 1 || actions.calls != 0 {
		t.Fatalf("health alert stats=%+v webhook_calls=%d action_calls=%d err=%v", stats, dispatch.callCount(), actions.calls, err)
	}
}
