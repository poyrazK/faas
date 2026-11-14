package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 127 — amendment 2026-11-05: the app's other events cannot push the
// scheduler's observations out of the running history.
func TestDebugRunning_OtherEventsDoNotCrowdOutObservations(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{
		AccountID: (*e.Acct()).ID,
		Slug:      "running-debug-busy",
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	now := time.Now().UTC()
	appendRunningEvent(t, e, app.ID, now.Add(-2*time.Hour), api.DebugRunningReasonRequestActivity)
	subject := app.ID
	for i := range 450 {
		at := now.Add(-time.Hour + time.Duration(i)*time.Second)
		if err := (*e.Store()).AppendEventAt(t.Context(), "schedd", "scale.decision", &subject, []byte(`{}`), at); err != nil {
			t.Fatalf("AppendEventAt: %v", err)
		}
	}

	rec := e.Do(t, http.MethodGet, "/v1/apps/running-debug-busy/debug/running?since=3h&limit=10", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var got api.DebugRunningResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.History) != 1 || got.HistoryTruncated {
		t.Fatalf("history = %+v truncated=%v, want the one observation behind 450 newer events", got.History, got.HistoryTruncated)
	}
}
