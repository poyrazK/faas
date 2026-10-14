package alerts_test

import (
	"context"
	"encoding/json"
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

type failingHealthStore struct {
	*state.MemStore
	incomplete bool
}

func (s *failingHealthStore) MCPConnectionHealth(ctx context.Context, account, app string, offset int, at time.Time) (api.MCPConnectionHealthSnapshot, error) {
	out, e := s.MemStore.MCPConnectionHealth(ctx, account, app, offset, at)
	if s.incomplete {
		out.Complete = false
	}
	return out, e
}
func TestMCPConnectionHealthAlertDedupeAndRecovery(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPOAuthReauthorization), state.AlertGt, 0)
	config := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}}}
	if _, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, config); err != nil {
		t.Fatal(err)
	}
	store := &failingHealthStore{MemStore: base}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	now := time.Now().UTC()
	build := func() *alerts.Evaluator {
		return alerts.NewEvaluator(alerts.EvaluatorOptions{Store: store, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	}
	evaluator := build()
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 || dispatch.callCount() != 1 {
		t.Fatal("health did not notify", stats, err)
	}
	if dispatch.calls[0].Payload["mcp_connection_health"] == nil || dispatch.calls[0].Payload["incident_id"] == "" {
		t.Fatal("missing health recovery evidence")
	}
	now = now.Add(time.Hour)
	evaluator = build()
	if _, err = evaluator.RunOnce(t.Context()); err != nil || dispatch.callCount() != 1 {
		t.Fatal("unchanged incident repeated after restart", err)
	}
	// Partial inventory cannot resolve a delivered incident.
	store.incomplete = true
	if _, err = evaluator.RunOnce(t.Context()); err != nil || dispatch.callCount() != 1 {
		t.Fatal("incomplete inventory recovered", err)
	}
	store.incomplete = false
	config.Enabled = false
	config.Observer.Enabled = false
	config.Revision = uuid.NewString()
	if _, err = base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, config); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err = evaluator.RunOnce(t.Context()); err != nil || dispatch.callCount() != 2 {
		t.Fatal("recovery was not delivered", err)
	}
	if dispatch.calls[1].Payload["event"] != "alert.resolved" || dispatch.calls[1].Payload["incident_id"] != dispatch.calls[0].Payload["incident_id"] {
		t.Fatal("recovery did not correlate incident")
	}
	now = now.Add(time.Hour)
	_, _ = build().RunOnce(t.Context())
	if dispatch.callCount() != 2 {
		t.Fatal("recovery repeated")
	}
	// The same role can produce a new incident after its prior recovery.
	config.Enabled = true
	config.Observer.Enabled = true
	config.Revision = uuid.NewString()
	_, _ = base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, config)
	now = now.Add(time.Hour)
	_, _ = build().RunOnce(t.Context())
	if dispatch.callCount() != 3 {
		t.Fatal("new incident suppressed by old fingerprint")
	}
	deliveries, _ := base.ListAlertDeliveriesForRule(t.Context(), rule.ID, 10, false)
	for _, delivery := range deliveries {
		var body map[string]any
		if json.Unmarshal(delivery.Payload, &body) != nil {
			t.Fatal("bad durable health payload")
		}
	}
}

func TestMCPConnectionHealthRecoveryRetryPreservesFiringCooldown(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPOAuthReauthorization), state.AlertGt, 0)
	cfg := api.MCPCatalogMonitorConfig{Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}}}
	if _, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: base, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog()})
	if _, err := evaluator.RunOnce(t.Context()); err != nil || dispatcher.callCount() != 1 {
		t.Fatal("first notification failed", err)
	}
	firing, _ := base.AlertRuleByID(t.Context(), rule.ID)
	cfg.Enabled = false
	cfg.Observer.Enabled = false
	cfg.Revision = uuid.NewString()
	if _, err := base.ConfigureMCPCatalogMonitor(t.Context(), rule.AccountID, rule.AppID, cfg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	dispatcher.result.StatusCode = 503
	if _, err := evaluator.RunOnce(t.Context()); err != nil || dispatcher.callCount() != 2 {
		t.Fatal("recovery ignored firing cooldown incorrectly", err)
	}
	recovered, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if !recovered.LastFiredAt.Equal(firing.LastFiredAt) || recovered.State != state.AlertStateOk {
		t.Fatal("recovery consumed firing cooldown or left incident firing")
	}
	now = now.Add(time.Second)
	_, _ = evaluator.RunOnce(t.Context())
	if dispatcher.callCount() != 2 {
		t.Fatal("failed recovery retried before cooldown")
	}
	now = now.Add(time.Hour)
	dispatcher.result.StatusCode = 200
	if _, err := evaluator.RunOnce(t.Context()); err != nil || dispatcher.callCount() != 3 {
		t.Fatal("failed recovery was not retried", err)
	}
	if dispatcher.calls[2].Payload["event"] != "alert.resolved" || dispatcher.calls[2].Payload["incident_id"] != dispatcher.calls[0].Payload["incident_id"] {
		t.Fatal("recovery retry lost incident identity")
	}
	now = now.Add(time.Hour)
	_, _ = evaluator.RunOnce(t.Context())
	if dispatcher.callCount() != 3 {
		t.Fatal("delivered recovery was repeated")
	}
}
