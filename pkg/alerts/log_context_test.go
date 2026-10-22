package alerts_test

// adr: 20261022172052 — a firing app alert carries what the log ledger shows:
// counts by level and status, the busiest error route, recent deploys and a
// log search link; nothing while the gate is off.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func fireWithLogContext(t *testing.T, store *state.MemStore, ident *age.X25519Identity, now time.Time, enabled bool) map[string]any {
	t.Helper()
	dispatcher := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: dispatcher,
		Now:        func() time.Time { return now },
		Log:        discardLog(),
	})
	ev.SetLogContext(enabled)
	if _, err := ev.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dispatcher.callCount() != 1 {
		t.Fatalf("dispatched %d webhooks, want 1", dispatcher.callCount())
	}
	return dispatcher.calls[0].Payload
}

func seedLogContextLedger(t *testing.T, store *state.MemStore, rule state.AlertRule, now time.Time) {
	t.Helper()
	for i, e := range []state.LogEvent{
		{Source: state.LogEventSourceHTTP, Status: 502, Level: "error", Route: "/checkout", Method: "POST", Message: "POST /checkout returned 502", Occurrences: 3},
		{Source: state.LogEventSourceHTTP, Status: 200, Level: "info", Route: "/health", Method: "GET", Message: "GET /health returned 200"},
		{Source: state.LogEventSourceRuntime, Level: "error", Message: "db timeout"},
		{Source: state.LogEventSourceRuntime, Level: "warn", Message: "slow query"},
	} {
		e.OccurredAt, e.AccountID, e.AppID, e.SourceEventID = now.Add(-time.Duration(i+1)*time.Minute), rule.AccountID, rule.AppID, "e"+string(rune('a'+i))
		if _, err := store.InsertLogEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.InsertLogEvent(context.Background(), state.LogEvent{
		OccurredAt: now.Add(-40 * time.Minute), AccountID: rule.AccountID, AppID: rule.AppID, Source: state.LogEventSourceDeploy,
		SourceEventID: "audit:1", Level: "info", Stream: "system", Message: "deploy traffic changed from 10% to 100%",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluator_FiringAlertCarriesLogContext(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	rule, ident, _ := seedLogSearchRule(t, store, "level=error", 1)
	seedLogContextLedger(t, store, rule, now)

	raw, ok := fireWithLogContext(t, store, ident, now, true)["log_context"]
	if !ok {
		t.Fatal("payload has no log_context")
	}
	var lc struct {
		Errors     int64                      `json:"errors"`
		Warnings   int64                      `json:"warnings"`
		HTTP5xx    int64                      `json:"http_5xx"`
		Route      struct{ Route string }     `json:"busiest_error_route"`
		Deploys    []struct{ Message string } `json:"recent_deploys"`
		SearchPath string                     `json:"search_path"`
	}
	encoded, _ := json.Marshal(raw)
	if err := json.Unmarshal(encoded, &lc); err != nil {
		t.Fatal(err)
	}
	if lc.Errors != 4 || lc.Warnings != 1 || lc.HTTP5xx != 3 || lc.Route.Route != "/checkout" ||
		len(lc.Deploys) != 1 || !strings.HasPrefix(lc.SearchPath, "/dashboard/apps/alert-app/log-search?") {
		t.Fatalf("log_context = %s", encoded)
	}
}

func TestEvaluator_LogContextOffByDefault(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	rule, ident, _ := seedLogSearchRule(t, store, "level=error", 1)
	seedLogContextLedger(t, store, rule, now)
	if _, ok := fireWithLogContext(t, store, ident, now, false)["log_context"]; ok {
		t.Fatal("log_context attached while the gate is off")
	}
}
