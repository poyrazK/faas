package alerts_test

import (
	"context"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"testing"
	"time"
)

type mcpConvergenceStore struct {
	*state.MemStore
	snapshot         api.MCPPolicyConvergenceSnapshot
	err              error
	accountID, appID string
	reads            int
}

func (s *mcpConvergenceStore) MCPPolicyConvergenceSnapshot(_ context.Context, accountID, appID string) (api.MCPPolicyConvergenceSnapshot, error) {
	s.accountID = accountID
	s.appID = appID
	s.reads++
	return s.snapshot, s.err
}
func TestMCPPolicyConvergenceAlertsFireDeduplicateAndResolve(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetricMCPPolicyUnconvergedGateways, state.AlertGt, 0)
	store := &mcpConvergenceStore{MemStore: base, snapshot: api.MCPPolicyConvergenceSnapshot{State: "pending", DesiredRevision: 42, ServingGateways: 2, UnconvergedGateways: 1, AdmissionID: "receipt-id", AdmissionHistoryPath: "/v1/deployments/target/audit", PolicyStatusPath: "/v1/apps/alert-app/policy/status?edge_rules_scope=account"}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	now := time.Now().UTC()
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatalf("fire: %+v %v", stats, err)
	}
	if store.accountID != rule.AccountID || store.appID != rule.AppID || store.reads != 1 {
		t.Fatal("unscoped or repeated observation")
	}
	payload := dispatch.calls[0].Payload["mcp_convergence"].(*api.MCPPolicyConvergenceSnapshot)
	if payload.AdmissionID != "receipt-id" || payload.UnconvergedGateways != 1 {
		t.Fatalf("correlation missing: %+v", payload)
	}
	now = now.Add(time.Minute)
	stats, err = evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 0 || dispatch.callCount() != 1 {
		t.Fatalf("duplicate delivered: %+v %v", stats, err)
	}
	store.snapshot.State = "active"
	store.snapshot.UnconvergedGateways = 0
	now = now.Add(time.Minute)
	stats, err = evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 0 {
		t.Fatal(err)
	}
	resolved, err := base.AlertRuleByID(t.Context(), rule.ID)
	if err != nil || resolved.State != state.AlertStateOk {
		t.Fatalf("not recovered: %+v %v", resolved, err)
	}
	logs, err := base.ListEvents(t.Context(), rule.AccountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range logs {
		if entry.Kind == "alert.resolved" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery audit not emitted")
	}
}
func TestMCPPolicyConvergenceUnknownAndPaused(t *testing.T) {
	for _, scenario := range []struct {
		name, signal string
		metric       state.AlertMetric
		value        int64
		err          error
		fired        int
		expected     state.AlertState
	}{
		{"missing fleet", "unverified", state.AlertMetricMCPPolicyUnverified, 0, nil, 1, state.AlertStateFiring},
		{"unknown revision", "unverified", state.AlertMetricMCPPolicyUnconvergedGateways, 0, nil, 0, state.AlertStateUnknown},
		{"no deployment", "paused", state.AlertMetricMCPPolicyUnverified, 0, nil, 0, state.AlertStateUnknown},
		{"database unavailable", "", state.AlertMetricMCPPolicyUnverified, 0, errors.New("PRIVATE_ERROR"), 0, state.AlertStateDegraded},
		{"invalid state", "bad", state.AlertMetricMCPPolicyUnverified, 0, nil, 0, state.AlertStateDegraded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base := state.NewMemStore()
			rule, identity, _ := seedRule(t, base, scenario.metric, state.AlertGt, 0)
			store := &mcpConvergenceStore{MemStore: base, snapshot: api.MCPPolicyConvergenceSnapshot{State: scenario.signal, UnconvergedGateways: scenario.value}, err: scenario.err}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200}}
			evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Log: discardLog()})
			stats, err := evaluator.RunOnce(t.Context())
			if err != nil || stats.Fired != scenario.fired {
				t.Fatalf("%+v %v", stats, err)
			}
			got, err := base.AlertRuleByID(t.Context(), rule.ID)
			if err != nil || got.State != scenario.expected {
				t.Fatalf("state %+v %v", got, err)
			}
		})
	}
}
