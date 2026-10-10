package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 938 — completeness is reported on demand and warned after a draft.
func TestCmdConsumersCompleteness(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	var since, until string
	base := "/v1/apps/my-api/consumers/c1"
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET " + base + "/usage-completeness":
			since, until = r.URL.Query().Get("since"), r.URL.Query().Get("until")
			_ = json.NewEncoder(w).Encode(api.APIConsumerUsageCompletenessResponse{ConsumerID: "c1", Status: "gaps_detected",
				CheckedFrom: start, CheckedUntil: end, LedgerRequests: 90, TelemetryRequests: 100, ConfirmedRequests: 90, MissingRequests: 10, HoursChecked: 3})
		case "POST " + base + "/usage-statements":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.APIConsumerUsageStatementResponse{ID: "s1", PeriodStart: start, PeriodEnd: end, Revision: 1, Status: "draft"})
		default:
			http.NotFound(w, r)
		}
	})
	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })

	if code := cmdConsumers([]string{"completeness", "my-api", "c1", "--month", "2026-09"}); code != 0 {
		t.Fatalf("completeness exit = %d", code)
	}
	if since != "2026-09-01T00:00:00Z" || until != "2026-10-01T00:00:00Z" {
		t.Fatalf("window = %s..%s", since, until)
	}
	if out := stdout.String(); !strings.Contains(out, "gaps_detected") || !strings.Contains(strings.Join(strings.Fields(out), " "), "Missing (at least) 10") {
		t.Fatalf("completeness output:\n%s", out)
	}
	if code := cmdConsumers([]string{"statement-draft", "my-api", "c1", "--month", "2026-09"}); code != 0 {
		t.Fatalf("statement-draft exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "at least 10 successful requests this statement does not bill") {
		t.Fatalf("draft warning missing; stderr:\n%s", stderr.String())
	}
	if code := cmdConsumers([]string{"completeness", "my-api", "c1"}); code == 0 {
		t.Fatal("completeness without a period succeeded")
	}
}
