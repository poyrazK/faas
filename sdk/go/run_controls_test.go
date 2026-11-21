package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	preview "github.com/gregale-dev/gregale-go/preview"
)

func TestRunControlsRevisionHistoryAndZeroLimitWireContract(t *testing.T) {
	sets, gets, deletes := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || r.Method == "GET" && r.Header.Get("Idempotency-Key") != "" || r.Method != "GET" && r.Header.Get("Idempotency-Key") == "" {
			t.Error("wrong authorization or unexpected invocation identity")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/run-controls/budgets/customer/customer-42":
			sets++
			var req preview.SetRunBudgetRequest
			if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision != 2 || req.Enabled || req.Limits.MaxConcurrent == nil || *req.Limits.MaxConcurrent != 0 {
				t.Error("zero limit or revision changed")
			}
			_ = json.NewEncoder(w).Encode(preview.RunBudgetPolicy{Revision: 3})
		case "/v1/run-history":
			gets++
			if r.URL.Query().Get("cursor") != "opaque/page+==" || r.URL.Query().Get("task_version") != "1" || r.URL.Query().Get("customer_ref") != "customer-42" || r.URL.Query().Get("since") != "2026-11-16T00:00:00Z" {
				t.Error("history filters changed")
			}
			_ = json.NewEncoder(w).Encode(preview.RunHistoryResponse{Runs: []preview.RunHistoryEntry{{ID: "run", Attribution: &preview.RunAttribution{CustomerRef: "customer-42"}}}, NextCursor: "next-page"})
		case "/v1/run-controls/bindings/api_key/public-key-id":
			deletes++
			if r.Method != "DELETE" || r.URL.Query().Get("expected_revision") != "3" {
				t.Error("delete revision changed")
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c, err := preview.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	p, err := c.SetRunBudget(context.Background(), "customer", "customer-42", preview.SetRunBudgetRequest{ExpectedRevision: 2, Enabled: false, Limits: preview.RunBudgetLimits{MaxConcurrent: &zero}})
	if err != nil || p.Revision != 3 {
		t.Fatal("budget response", p, err)
	}
	page, err := c.ListRunHistory(context.Background(), preview.RunHistoryFilter{TaskName: "report", TaskVersion: 1, CustomerRef: "customer-42", Cursor: "opaque/page+==", Since: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)})
	if err != nil || len(page.Runs) != 1 || page.Runs[0].Attribution.CustomerRef != "customer-42" || page.NextCursor != "next-page" {
		t.Fatal("history response", page, err)
	}
	if err := c.DeleteRunAttributionBinding(context.Background(), "api_key", "public-key-id", 3); err != nil {
		t.Fatal(err)
	}
	if sets != 1 || gets != 1 || deletes != 1 {
		t.Fatal("requests repeated", sets, gets, deletes)
	}
	for _, raw := range []string{`{"expected_revision":2,"enabled":true}`, `{"expected_revision":2,"enabled":true,"limits":{"max_concurent":1}}`} {
		var req preview.SetRunBudgetRequest
		if json.Unmarshal([]byte(raw), &req) == nil {
			t.Fatal("incomplete replacement decoded", raw)
		}
	}
}
