package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func historyRules(now time.Time, firing bool) []promql.AlertRuleGroup {
	r := promql.AlertRule{Type: "alerting", Name: "FaasApiDown", Query: "up == 0", Duration: 60, Health: "ok", State: "inactive", LastEvaluation: now, Alerts: []promql.Alert{}, Labels: map[string]string{"severity": "page"}, Annotations: map[string]string{"runbook_url": "/docs/runbooks/FaasApiDown.md"}}
	if firing {
		r.State = "firing"
		r.Alerts = []promql.Alert{{State: "firing", ActiveAt: now.Add(-time.Minute), Labels: map[string]string{"alertname": "FaasApiDown", "severity": "page", "component": "apid"}}}
	}
	return []promql.AlertRuleGroup{{Name: "faas", File: "/etc/prometheus/faas.rules.yml", Rules: []promql.AlertRule{r}}}
}

func TestAlertHistoryCollectorLifecycleAndOutage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	now := time.Now().UTC().Truncate(time.Microsecond)
	groups := historyRules(now, true)
	var current atomic.Value
	current.Store(groups)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/rules" || r.URL.Query().Get("type") != "alert" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": current.Load().([]promql.AlertRuleGroup)}})
	}))
	defer srv.Close()
	client := promql.NewClient(srv.URL, srv.Client())
	if err := CollectAlertsOnce(ctx, store, client, now); err != nil {
		t.Fatal(err)
	}
	items, missing, err := AlertIncidents(ctx, store, nil, state.OperatorAlertFilter{Now: now, Since: now.Add(-time.Hour), Limit: 10})
	if err != nil || len(items) != 1 || len(missing) != 0 || items[0].Status != "firing" || items[0].AlertEpisode == nil {
		t.Fatalf("items=%+v missing=%v err=%v", items, missing, err)
	}
	first := items[0]
	id := strings.TrimPrefix(first.ID, "platform_alert/")
	before := calls.Load()
	evidence, err := Evidence(ctx, store, nil, "platform_alert", id, 1, now)
	if err != nil || evidence.Incident.ID != first.ID || len(evidence.History) != 1 || calls.Load() != before {
		t.Fatalf("retained evidence=%+v err=%v", evidence, err)
	}
	if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, err = Evidence(ctx, store, nil, "platform_alert", id, 25, now.Add(time.Second))
	if err != nil || evidence.Incident.Status != "unknown" || len(evidence.UnavailableSources) != 1 || evidence.Incident.AlertEpisode.ResolvedAt != nil {
		t.Fatalf("outage evidence=%+v err=%v", evidence, err)
	}
	items, missing, err = AlertIncidents(ctx, store, nil, state.OperatorAlertFilter{Now: now.Add(time.Second), Limit: 10})
	if err != nil || len(items) != 1 || items[0].Status != "unknown" || len(missing) != 1 {
		t.Fatalf("outage inbox=%+v missing=%v err=%v", items, missing, err)
	}
	groups = historyRules(now.Add(2*time.Second), false)
	current.Store(groups)
	if err := CollectAlertsOnce(ctx, store, client, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	evidence, err = Evidence(ctx, store, nil, "platform_alert", id, 1, now.Add(2*time.Second))
	if err != nil || evidence.Incident.Status != "resolved" || len(evidence.History) != 1 || !evidence.Truncated || evidence.History[0].Kind != "platform_alert.resolved" || evidence.Incident.AlertEpisode.ObservationState != "historical" {
		t.Fatalf("resolved evidence=%+v err=%v", evidence, err)
	}
	alias, err := Evidence(ctx, store, nil, "platform_alert", first.AlertEpisode.SeriesID, 25, now.Add(2*time.Second))
	if err != nil || alias.Incident.ID != first.ID || len(alias.History) != 2 {
		t.Fatalf("legacy link=%+v err=%v", alias, err)
	}
	groups = historyRules(now.Add(3*time.Second), true)
	groups[0].Rules[0].Alerts[0].ActiveAt = now.Add(3 * time.Second)
	current.Store(groups)
	if err := CollectAlertsOnce(ctx, store, client, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, _, err = AlertIncidents(ctx, store, nil, state.OperatorAlertFilter{Now: now.Add(3 * time.Second), Limit: 10})
	if err != nil || len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("recurrence inbox=%+v err=%v", items, err)
	}
	// Old evidence remains attached to its original resolved episode.
	old, err := Evidence(ctx, store, nil, "platform_alert", id, 25, now.Add(3*time.Second))
	if err != nil || old.Incident.Status != "resolved" {
		t.Fatalf("old episode=%+v err=%v", old, err)
	}
}

func TestAlertHistoryIncompleteSourcesNeverResolve(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, name := range []string{"failed HTTP", "malformed", "malformed timestamp", "missing groups", "empty rules", "warning", "missing alerts", "stale", "future", "evaluation error", "inconsistent state", "missing identity", "changed rule"} {
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			initial, err := alertSnapshot(historyRules(now, true), now)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.RecordOperatorAlertSnapshot(context.Background(), initial); err != nil {
				t.Fatal(err)
			}
			next := now.Add(time.Second)
			groups := historyRules(next, false)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				switch name {
				case "failed HTTP":
					http.Error(w, "private-payload", 503)
					return
				case "malformed":
					_, _ = w.Write([]byte(`{`))
					return
				case "malformed timestamp":
					_, _ = w.Write([]byte(`{"status":"success","data":{"groups":[{"name":"platform","file":"rules.yml","rules":[{"lastEvaluation":"private-payload"}]}]}}`))
					return
				case "missing groups":
					_, _ = w.Write([]byte(`{"status":"success","data":{}}`))
					return
				case "empty rules":
					groups = []promql.AlertRuleGroup{}
				case "warning":
					_, _ = w.Write([]byte(`{"status":"success","warnings":["truncated"],"data":{"groups":[]}}`))
					return
				case "missing alerts":
					groups[0].Rules[0].Alerts = nil
				case "stale":
					groups[0].Rules[0].LastEvaluation = now.Add(-time.Hour)
				case "future":
					groups[0].Rules[0].LastEvaluation = now.Add(time.Hour)
				case "evaluation error":
					groups[0].Rules[0].Health = "err"
					groups[0].Rules[0].LastError = "private-error"
				case "inconsistent state":
					groups[0].Rules[0].State = "firing"
				case "missing identity":
					groups[0].File = ""
				case "changed rule":
					groups[0].Rules[0].Query = "different expression"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": groups}})
			}))
			defer srv.Close()
			err = CollectAlertsOnce(context.Background(), store, promql.NewClient(srv.URL, srv.Client()), next)
			if name != "changed rule" && err == nil {
				t.Fatal("incomplete source accepted")
			}
			e, c, readErr := store.OperatorAlertEpisode(context.Background(), initial.Episodes[0].ID)
			if readErr != nil || e.ResolvedAt != nil || e.Status(c, next) != "unknown" {
				t.Fatalf("false recovery e=%+v c=%+v err=%v", e, c, readErr)
			}
			if err != nil && strings.Contains(err.Error(), "private-") {
				t.Fatal("collector exposed raw error payload")
			}
		})
	}
}

func TestAlertHistorySafeProjectionAndExclusions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	groups := historyRules(now, true)
	a := &groups[0].Rules[0].Alerts[0]
	a.Labels["component"] = "private-component\n"
	a.Labels["account_id"] = "../../secret"
	a.Labels["secret"] = "private-payload"
	a.Annotations = map[string]string{"runbook_url": "https://evil.example/secret", "description": "private-payload"}
	snapshot, err := alertSnapshot(groups, now)
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	if err := store.RecordOperatorAlertSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	item := retainedIncident(snapshot.Episodes[0], state.OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}, now)
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "evil.example") || item.AccountID != "" {
		t.Fatalf("unsafe projection %s", encoded)
	}
	data, _ := json.Marshal(snapshot.Episodes)
	if strings.Contains(string(data), "private-") {
		t.Fatalf("raw metadata persisted: %s", data)
	}
	for _, name := range []string{"watchdog", "info"} {
		g := historyRules(now, true)
		if name == "watchdog" {
			g[0].Rules[0].Name = "FaasWatchdog"
			g[0].Rules[0].Alerts[0].Labels["alertname"] = "FaasWatchdog"
		} else {
			g[0].Rules[0].Alerts[0].Labels["severity"] = "info"
		}
		s, err := alertSnapshot(g, now)
		if err != nil || len(s.Episodes) != 0 {
			t.Fatalf("excluded %s retained: %+v %v", name, s, err)
		}
	}
	// Once collection has started, a missing episode is an honest 404 even if
	// Prometheus is offline; it does not trigger a hidden live fallback.
	_, err = Evidence(context.Background(), store, nil, "platform_alert", strings.Repeat("f", 64), 25, now)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing retained evidence: %v", err)
	}
}

func TestAlertTriageFilteredFallbackRequiresRetainedHistory(t *testing.T) {
	store := state.NewMemStore()
	items, missing, err := AlertIncidents(context.Background(), store, nil, state.OperatorAlertFilter{Now: time.Now().UTC(), Limit: 10, Acknowledgment: "unacknowledged"})
	if err != nil || len(items) != 0 || len(missing) != 1 || missing[0] != "platform_alert_history" {
		t.Fatalf("unretained triage: items=%+v missing=%v err=%v", items, missing, err)
	}
}
