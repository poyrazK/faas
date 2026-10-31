package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021195901 — customers trial moves a running trial's end with
// PUT, or ends it with DELETE, and takes exactly one of --ends-at or --end.
func TestProductCustomerTrialCLI(t *testing.T) {
	tenant := uuid.NewString()
	var requests []string
	var body api.ProductTrialRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPut {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		_ = json.NewEncoder(w).Encode(api.ProductCustomer{TenantID: tenant, Plan: "pro"})
	}))
	defer srv.Close()
	run := func(args ...string) {
		t.Helper()
		c, err := main.ParseProductCommand(append([]string{"customers", "trial", "--app", "exports", "--tenant", tenant}, args...))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := main.RunProductCommand(t.Context(), main.NewClient(srv.URL, "owner"), c); err != nil {
			t.Fatal(err)
		}
	}
	run("--ends-at", "2026-11-15T00:00:00Z")
	run("--end")
	path := "/v1/apps/exports/product/customers/" + tenant + "/trial"
	if len(requests) != 2 || requests[0] != "PUT "+path || requests[1] != "DELETE "+path || !body.EndsAt.Equal(time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("requests = %v, body %+v", requests, body)
	}
	for _, args := range [][]string{
		{"customers", "trial", "--app", "exports", "--tenant", tenant},
		{"customers", "trial", "--app", "exports", "--tenant", tenant, "--end", "--ends-at", "2026-11-15T00:00:00Z"},
		{"customers", "trial", "--app", "exports", "--tenant", tenant, "--ends-at", "next week"},
		{"customers", "trial", "--app", "exports", "--tenant", "alice", "--end"},
	} {
		if _, err := main.ParseProductCommand(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}
