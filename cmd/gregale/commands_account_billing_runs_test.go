package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1503 — start waits for the run, then lists every customer's outcome;
// --no-X flags send explicit false and the schedule verbs round-trip.
func TestCmdAccountBillingRun(t *testing.T) {
	t.Setenv("FAAS_API_MONETIZATION_ENABLED", "1") // adr: 20261018090210 — exercises released monetization
	var created api.CreateBillingRunRequest
	var sched api.PutBillingRunScheduleRequest
	polls := 0
	amount := int64(3750)
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/billing-runs":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.BillingRunResponse{ID: "run1", Month: created.Month, Status: "queued", Export: created.Export})
		case "GET /v1/account/billing-runs/run1":
			polls++
			status := "running"
			if polls > 1 {
				status = "completed"
			}
			_ = json.NewEncoder(w).Encode(api.BillingRunResponse{ID: "run1", Month: "2026-09", Status: status, Export: "stripe",
				Counts: api.BillingRunCounts{Exported: 1, Held: 1}})
		case "GET /v1/account/billing-runs/run1/items":
			_ = json.NewEncoder(w).Encode(api.BillingRunItemListResponse{Items: []api.BillingRunItemResponse{
				{SourceKind: "app_consumer", ExternalRef: "cus_Paying1", Outcome: "exported", Currency: "EUR", AmountMillicents: &amount, InvoiceID: "in_1"},
				{SourceKind: "app_consumer", ExternalRef: "cus_Unpriced1", Outcome: "held", Reason: "add a rate card"},
			}})
		case "PUT /v1/account/billing-run-schedule":
			_ = json.NewDecoder(r.Body).Decode(&sched)
			_ = json.NewEncoder(w).Encode(api.BillingRunScheduleResponse{NextMonth: "2026-10", DelayHours: *sched.DelayHours, Export: sched.Export,
				NextRunAt: time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)})
		case "DELETE /v1/account/billing-run-schedule":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	(*main.BillingRunPollInterval) = time.Millisecond
	t.Cleanup(func() { (*main.BillingRunPollInterval) = 5 * time.Second })

	if code := main.CmdAccount([]string{"billing-run", "start", "--month", "2026-09", "--export", "stripe", "--no-hold-on-gaps", "--wait"}); code != 0 {
		t.Fatalf("start exit = %d", code)
	}
	if created.Month != "2026-09" || created.Export != "stripe" || created.HoldOnGaps == nil || *created.HoldOnGaps || created.Finalize != nil {
		t.Fatalf("create body = %+v", created)
	}
	out := stdout.String()
	if polls < 2 || !strings.Contains(out, "exported 1") || !strings.Contains(out, "cus_Unpriced1") || !strings.Contains(out, "add a rate card") ||
		!strings.Contains(out, "EUR 0.0375") {
		t.Fatalf("polls = %d, output = %q", polls, out)
	}
	if code := main.CmdAccount([]string{"billing-run", "schedule-set", "--export", "stripe", "--delay-hours", "6"}); code != 0 {
		t.Fatalf("schedule-set exit = %d", code)
	}
	if sched.DelayHours == nil || *sched.DelayHours != 6 || sched.Export != "stripe" || !strings.Contains(stdout.String(), "2026-10") {
		t.Fatalf("schedule body = %+v, output = %q", sched, stdout.String())
	}
	if code := main.CmdAccount([]string{"billing-run", "schedule-delete"}); code != 0 {
		t.Fatalf("schedule-delete exit = %d", code)
	}
	if code := main.CmdAccount([]string{"billing-run", "show"}); code != 1 {
		t.Fatalf("show without RUN_ID exit = %d, want 1", code)
	}
}
