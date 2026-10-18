package alerts_test

import (
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"sync"
	"testing"
	"time"
)

type readinessAlertStore struct {
	*state.MemStore
	statuses  []api.MCPReadinessMonitorStatus
	err       error
	selectors []string
}

func (s *readinessAlertStore) ListMCPReadinessMonitoring(_ context.Context, _ string, _ string, _ string, scopes ...string) ([]api.MCPReadinessMonitorStatus, error) {
	selector := "*"
	if len(scopes) > 0 {
		selector = scopes[0]
	}
	s.selectors = append(s.selectors, selector)
	return s.statuses, s.err
}
func TestMCPReadinessMonitoringAlerts(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	now := time.Now().UTC()
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Role: "reader", NextProbeAt: now.Add(time.Minute), FailureThreshold: 2, ConsecutiveFailures: 1}}}
	dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog()})
	stats, err := evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 0 {
		t.Fatal("transient failure fired", stats, err)
	}
	source.statuses[0].ConsecutiveFailures = 2
	now = now.Add(time.Second)
	stats, err = evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 {
		t.Fatal("threshold did not fire", stats, err)
	}
	if dispatcher.calls[0].Payload["mcp_readiness"] == nil {
		t.Fatal("readiness evidence missing from alert")
	}
	source.err = errors.New("controlled outage")
	now = now.Add(30 * time.Second)
	_, _ = evaluator.RunOnce(t.Context())
	retained, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if retained.State != state.AlertStateDegraded || dispatcher.callCount() != 1 {
		t.Fatal("store outage reported recovery or sent another notification")
	}
	source.err = nil
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].LastSuccessAt = &now
	source.statuses[0].NextProbeAt = now.Add(time.Minute)
	_, _ = evaluator.RunOnce(t.Context())
	resolved, _ := base.AlertRuleByID(t.Context(), rule.ID)
	if resolved.State != state.AlertStateOk {
		t.Fatal("healthy result did not resolve")
	}
	now = now.Add(40 * time.Minute)
	stats, err = evaluator.RunOnce(t.Context())
	if err != nil || stats.Fired != 1 {
		t.Fatal("stalled scheduler was not alerted", stats, err)
	}
}

func TestMCPReadinessEnvironmentAlertIsolation(t *testing.T) {
	base := state.NewMemStore()
	legacy, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	create := func(name, scope, url string, threshold float64) state.AlertRule {
		r := legacy
		r.ID = ""
		r.Name = name
		r.MCPReadinessScope = scope
		r.WebhookURL = url
		r.Threshold = threshold
		saved, e := base.CreateAlertRule(t.Context(), r)
		if e != nil {
			t.Fatal(e)
		}
		return saved
	}
	prod := create("production readiness", "default", "https://prod.example/hook", 0)
	stage := create("staging readiness", "staging", "https://stage.example/hook", 0)
	highStage := create("staging high threshold", "staging", "https://stage-high.example/hook", 1)
	now := time.Now().UTC()
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{
		{Scope: "", DeploymentID: "prod-deployment", PolicyRevision: "prod-policy", Role: "reader", NextProbeAt: now.Add(time.Minute), FailureThreshold: 2, LastSuccessAt: &now},
		{Scope: "staging", DeploymentID: "stage-deployment", PolicyRevision: "stage-policy", Role: "reader", NextProbeAt: now.Add(time.Minute), FailureThreshold: 2, ConsecutiveFailures: 2},
	}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Audit: audit.New(base, discardLog(), nil, "meterd"), Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	stats, e := evaluator.RunOnce(t.Context())
	if e != nil || stats.Fired != 2 {
		t.Fatal("staging should fire its own and the legacy app-wide rule", stats, e)
	}
	for _, call := range dispatch.calls {
		if call.URL == prod.WebhookURL || call.URL == highStage.WebhookURL {
			t.Fatal("staging polluted another environment or threshold", call.URL)
		}
	}
	selectors := map[string]bool{}
	for _, s := range source.selectors {
		selectors[s] = true
	}
	if !selectors["*"] || !selectors["default"] || !selectors["staging"] {
		t.Fatal("missing store scope selection", selectors)
	}
	// A production failure must notify independently while staging is cooling down.
	now = now.Add(30 * time.Second)
	source.statuses[0].ConsecutiveFailures = 2
	stats, e = evaluator.RunOnce(t.Context())
	if e != nil || stats.Fired != 1 || dispatch.callCount() != 3 {
		t.Fatal("environment cooldowns not independent", stats, e)
	}
	call := dispatch.calls[2]
	if call.URL != prod.WebhookURL || call.Payload["mcp_readiness_scope"] != "default" || call.Payload["app_id"] != prod.AppID {
		t.Fatal("wrong production destination or context", call)
	}
	var statuses []api.MCPReadinessMonitorStatus
	body, marshalErr := json.Marshal(call.Payload["mcp_readiness"])
	if marshalErr != nil || json.Unmarshal(body, &statuses) != nil || len(statuses) != 1 || statuses[0].Scope != "default" || statuses[0].DeploymentID != "prod-deployment" || statuses[0].PolicyRevision != "prod-policy" || statuses[0].Role != "reader" {
		t.Fatal("notification leaked other environment or omitted subject", call.Payload)
	}
	retained, e := base.AlertRuleByID(t.Context(), highStage.ID)
	if e != nil || retained.State != state.AlertStateOk {
		t.Fatal("staging threshold counted production", retained, e)
	}
	source.err = errors.New("controlled monitoring outage")
	now = now.Add(30 * time.Second)
	_, _ = evaluator.RunOnce(t.Context())
	for _, id := range []string{prod.ID, stage.ID, legacy.ID} {
		r, e := base.AlertRuleByID(t.Context(), id)
		if e != nil || r.State != state.AlertStateDegraded {
			t.Fatal("outage reported recovery", r, e)
		}
	}
	source.err = nil
	for i := range source.statuses {
		source.statuses[i].ConsecutiveFailures = 0
		source.statuses[i].LastSuccessAt = &now
		source.statuses[i].NextProbeAt = now.Add(time.Minute)
	}
	_, _ = evaluator.RunOnce(t.Context())
	for _, id := range []string{prod.ID, stage.ID, legacy.ID} {
		r, e := base.AlertRuleByID(t.Context(), id)
		if e != nil || r.State != state.AlertStateOk {
			t.Fatal("environment did not recover", r, e)
		}
	}
	if dispatch.callCount() != 6 {
		t.Fatal("missing recovery events", dispatch.callCount())
	}
	for _, call := range dispatch.calls[3:] {
		if call.Payload["event"] != "alert.resolved" {
			t.Fatal("recovery dispatched a false fire", call.Payload)
		}
	}
}

func TestMCPReadinessIncidentRetryRestartAndCoverage(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	now := time.Now().UTC()
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "staging", DeploymentID: "deployment", PolicyRevision: "policy", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Minute)}}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 503, Attempts: 1}}
	run := func() {
		t.Helper()
		now = now.Add(time.Microsecond)
		e := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
		if _, err := e.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	run()
	first := dispatch.calls[0]
	if first.Signer == nil || first.Payload["event"] != "alert.fired" || first.Payload["event_id"] != first.EventID {
		t.Fatal("signed event identity", first)
	}
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].LastSuccessAt = &now
	source.statuses[0].DeploymentID = "replacement"
	source.statuses[0].PolicyRevision = "replacement-policy"
	run()
	source.statuses[0].ConsecutiveFailures = 1
	run()
	page, err := base.ListMCPReadinessIncidents(t.Context(), rule.AccountID, rule.AppID, "staging", "", 20)
	if err != nil || len(page.Events) != 3 || dispatch.callCount() != 1 {
		t.Fatal("failed transport blocked history", page, err)
	}
	if page.Events[2].IncidentID != page.Events[1].IncidentID || page.Events[0].IncidentID == page.Events[1].IncidentID || page.Events[1].IncidentStatus != "resolved" || page.Events[1].DeliveryStatus != "pending" {
		t.Fatal("incident state coupled to delivery", page)
	}
	// Restart on every tick, then let the queue catch up in order during an
	// independent monitor outage. No new observation is needed to retry a fact.
	source.err = errors.New("monitor unavailable")
	now = now.Add(time.Hour)
	dispatch.result = webhookout.Result{StatusCode: 200, Attempts: 1}
	for range 3 {
		run()
	}
	if dispatch.callCount() != 4 || dispatch.calls[1].EventID != first.EventID || dispatch.calls[2].EventID != page.Events[1].EventID || dispatch.calls[3].EventID != page.Events[0].EventID {
		t.Fatal("queue order or restart identity")
	}
	run()
	if dispatch.callCount() != 4 {
		t.Fatal("delivered notification repeated")
	}
	source.err = nil
	source.statuses[0].NextProbeAt = now.Add(time.Minute)
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].Role = "different-role"
	run()
	original := source.statuses
	source.statuses = []api.MCPReadinessMonitorStatus{}
	run()
	source.statuses = original
	if dispatch.callCount() != 4 {
		t.Fatal("missing role or monitor outage resolved incident")
	}
	source.statuses[0].Role = "reader"
	dispatch.result = webhookout.Result{StatusCode: 503, Attempts: 1}
	run()
	if dispatch.callCount() != 5 || dispatch.calls[4].Payload["event"] != "alert.resolved" || dispatch.calls[4].Payload["incident_id"] != page.Events[0].IncidentID {
		t.Fatal("matching recovery", dispatch.callCount())
	}
	now = now.Add(time.Hour)
	source.statuses[0].NextProbeAt = now.Add(time.Minute)
	dispatch.result = webhookout.Result{StatusCode: 200, Attempts: 1}
	run()
	if dispatch.callCount() != 6 || dispatch.calls[5].EventID != dispatch.calls[4].EventID {
		t.Fatal("recovery retry changed identity")
	}
	final, err := base.ListMCPReadinessIncidents(t.Context(), rule.AccountID, rule.AppID, "", "", 20)
	if err != nil || len(final.Events) != 4 {
		t.Fatal("attempts duplicated logical events", final, err)
	}
}

type blockedReadinessDispatcher struct {
	entered  chan struct{}
	release  chan struct{}
	recorder *recordingDispatcher
}

func (d *blockedReadinessDispatcher) Dispatch(ctx context.Context, target webhookout.Target, event webhookout.Event) webhookout.Result {
	close(d.entered)
	select {
	case <-d.release:
	case <-ctx.Done():
		return webhookout.Result{Err: ctx.Err()}
	}
	return d.recorder.Dispatch(ctx, target, event)
}
func TestMCPReadinessObservationsWhileDeliveryBlocked(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	now := time.Now().UTC()
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "default", DeploymentID: "deployment", PolicyRevision: "policy", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Minute)}}}
	blocked := &blockedReadinessDispatcher{entered: make(chan struct{}), release: make(chan struct{}), recorder: &recordingDispatcher{result: webhookout.Result{StatusCode: 503, Attempts: 1}}}
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(blocked.release) }) })
	makeEvaluator := func(dispatch alerts.Dispatcher) *alerts.Evaluator {
		return alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
	}
	done := make(chan error, 1)
	go func() { _, err := makeEvaluator(blocked).RunOnce(t.Context()); done <- err }()
	select {
	case <-blocked.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("delivery did not start")
	}
	other := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	now = now.Add(time.Second)
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].LastSuccessAt = &now
	if _, err := makeEvaluator(other).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	source.statuses[0].ConsecutiveFailures = 1
	if _, err := makeEvaluator(other).RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := base.ListMCPReadinessIncidents(t.Context(), rule.AccountID, rule.AppID, "", "", 20)
	if err != nil || len(page.Events) != 3 || other.callCount() != 0 {
		t.Fatal("blocked delivery suppressed observations or duplicate lease", page, err)
	}
	release.Do(func() { close(blocked.release) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestMCPReadinessMaintenanceEvaluatorRestart(t *testing.T) {
	base := state.NewMemStore()
	rule, identity, _ := seedRule(t, base, state.AlertMetric(api.MetricMCPReadinessUnhealthy), state.AlertGt, 0)
	now := time.Now().UTC()
	_, err := base.CreateMCPReadinessMaintenance(t.Context(), rule.AccountID, rule.AppID, api.CreateMCPReadinessMaintenanceRequest{Scope: "default", EndsAt: now.Add(time.Minute), Reason: "planned work"}, now)
	if err != nil {
		t.Fatal(err)
	}
	source := &readinessAlertStore{MemStore: base, statuses: []api.MCPReadinessMonitorStatus{{Scope: "default", Role: "reader", FailureThreshold: 1, ConsecutiveFailures: 1, NextProbeAt: now.Add(time.Hour)}}}
	dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	tick := func() {
		t.Helper()
		e := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: source, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatcher, Now: func() time.Time { return now }, Log: discardLog()})
		if _, err := e.RunOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	if dispatcher.callCount() != 0 {
		t.Fatal("muted fire sent")
	}
	now = now.Add(2 * time.Minute)
	source.err = errors.New("monitor outage")
	tick()
	if dispatcher.callCount() != 0 {
		t.Fatal("outage invented expiry notification")
	}
	now = now.Add(time.Second)
	source.err = nil
	tick()
	if dispatcher.callCount() != 1 || dispatcher.calls[0].Payload["notification_reason"] != "maintenance_ended" {
		t.Fatal("missing continuation notification", dispatcher.calls)
	}
	now = now.Add(time.Second)
	tick()
	if dispatcher.callCount() != 1 {
		t.Fatal("continued incident repeated")
	}
	now = now.Add(time.Second)
	source.statuses[0].ConsecutiveFailures = 0
	source.statuses[0].LastSuccessAt = &now
	tick()
	if dispatcher.callCount() != 2 || dispatcher.calls[1].Payload["event"] != "alert.resolved" {
		t.Fatal("missing recovery", dispatcher.calls)
	}
	page, err := base.ListMCPReadinessIncidents(t.Context(), rule.AccountID, rule.AppID, "default", "", 20)
	if err != nil || len(page.Events) != 3 || page.Events[2].DeliveryStatus != "suppressed" {
		t.Fatal("muted event lost", page, err)
	}
}
