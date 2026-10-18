package alerts_test

import (
	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"testing"
	"time"
)

func TestMCPCatalogMonitorAlertsAndRecovery(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPCatalogDrift), state.AlertGt, 0)
	revision := uuid.NewString()
	_, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogMonitorConfig{Enabled: true, Revision: revision, FreshnessSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := base.RecordMCPCatalogObservation(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogReceipt{ID: uuid.NewString(), Revision: revision, State: "drift", ChangedCount: 2, ObservedSHA256: "drift"})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	now := time.Now().UTC()
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: base, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 {
		t.Fatalf("fire: %+v %v", stats, err)
	}
	payload := dispatch.calls[0].Payload["mcp_catalog"].(*api.MCPCatalogMonitorSignal)
	if payload.ReceiptID != receipt.ID || payload.ChangedCount != 2 || payload.EvidencePath == "" {
		t.Fatal("retained evidence link missing")
	}
	now = now.Add(30 * time.Second)
	_, _ = evaluator.RunOnce(t.Context())
	if dispatch.callCount() != 1 {
		t.Fatal("cooldown ignored")
	}
	_, err = base.RecordMCPCatalogObservation(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogReceipt{ID: uuid.NewString(), Revision: revision, State: "healthy", ObservedSHA256: "baseline"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	_, err = evaluator.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if resolved.State != state.AlertStateOk {
		t.Fatal("healthy catalog did not resolve drift")
	}
	events, _ := base.ListEvents(t.Context(), rule.AccountID, 100)
	found := false
	for _, event := range events {
		if event.Kind == "alert.resolved" {
			found = true
		}
	}
	if !found {
		t.Fatal("recovery audit absent")
	}
	now = now.Add(10 * time.Minute)
	_, _ = evaluator.RunOnce(t.Context())
	stale, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if stale.State != state.AlertStateUnknown {
		t.Fatal("stale catalog cleared drift as healthy")
	}
}
func TestMCPCatalogMonitorUnavailableAlert(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPCatalogMonitorUnavailable), state.AlertGt, 0)
	_, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogMonitorConfig{Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: base, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Log: discardLog()})
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 {
		t.Fatalf("missing observation not alerted: %+v %v", stats, err)
	}
}

func TestMCPCatalogMonitorMixedRoleAlert(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPCatalogDrift), state.AlertGt, 0)
	for _, role := range []string{"reader", "admin", "writer"} {
		revision := uuid.NewString()
		_, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogMonitorConfig{Role: role, Enabled: true, Revision: revision, FreshnessSeconds: 300})
		if err != nil {
			t.Fatal(err)
		}
		if role == "writer" {
			continue
		}
		stateName := "healthy"
		count := 0
		if role == "admin" {
			stateName = "drift"
			count = 3
		}
		_, err = base.RecordMCPCatalogObservation(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogReceipt{Role: role, ID: uuid.NewString(), Revision: revision, State: stateName, ChangedCount: count})
		if err != nil {
			t.Fatal(err)
		}
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: base, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Log: discardLog()})
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 {
		t.Fatalf("known drift hidden by unavailable role: %+v %v", stats, err)
	}
	payload := dispatch.calls[0].Payload["mcp_catalog"].(*api.MCPCatalogMonitorSignal)
	if payload.State != "unavailable" || payload.ChangedCount != 3 || len(payload.DriftedRoles) != 1 || payload.DriftedRoles[0] != "admin" || len(payload.UnavailableRoles) != 1 || payload.UnavailableRoles[0] != "writer" || payload.Roles["admin"].EvidencePath == "" {
		t.Fatalf("affected roles missing: %+v", payload)
	}
	threshold := float64(5)
	_, err = base.UpdateAlertRule(t.Context(), rule.ID, state.UpdateAlertRuleParams{Threshold: &threshold})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = evaluator.RunOnce(t.Context())
	unknown, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if unknown.State != state.AlertStateUnknown {
		t.Fatal("partial drift below threshold resolved while another role was unknown")
	}
	threshold = 0
	_, err = base.UpdateAlertRule(t.Context(), rule.ID, state.UpdateAlertRuleParams{Threshold: &threshold})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := base.GetMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID)
	_, err = base.RecordMCPCatalogObservation(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogReceipt{Role: "admin", ID: uuid.NewString(), Revision: snapshot.Roles["admin"].Config.Revision, State: "healthy", ObservedSHA256: "recovered"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = evaluator.RunOnce(t.Context())
	unresolved, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if unresolved.State != state.AlertStateUnknown {
		t.Fatal("missing writer cleared aggregate drift as healthy")
	}
	_, err = base.RecordMCPCatalogObservation(t.Context(), rule.AccountID, rule.AppID, api.MCPCatalogReceipt{Role: "writer", ID: uuid.NewString(), Revision: snapshot.Roles["writer"].Config.Revision, State: "healthy"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = evaluator.RunOnce(t.Context())
	resolved, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if resolved.State != state.AlertStateOk {
		t.Fatal("all roles did not recover")
	}
}
