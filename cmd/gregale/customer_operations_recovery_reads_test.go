// adr: 641
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomerOperationRecoveryReadsNeverApply(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer account" {
			t.Error("account credentials missing")
		}
		w.Header().Set("Content-Type", "application/json")
		i := api.OperationRecoveryInspection{OperationID: "op", Generation: 1, State: api.OperationRequiresReconciliation, InspectionRevision: "sha256:" + strings.Repeat("a", 64),
			Steps: []api.OperationRecoveryStep{{Name: "collect", State: "succeeded", Confirmed: true}, {Name: "finish", State: "dead", OutcomeUnknown: true}}}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/exports/operations/op/recovery-inspection":
			_ = json.NewEncoder(w).Encode(i)
		case "POST /v1/apps/exports/operations/op/recovery-preview":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 2 || body["expected_generation"] != float64(1) || body["resolution"] != "safe_to_retry" {
				t.Error("preview sent a decision or unfenced request", body, err)
			}
			_ = json.NewEncoder(w).Encode(api.OperationRecoveryPreview{Inspection: i, Resolution: "safe_to_retry", Eligible: false, EvidenceRequired: true, Blockers: []string{"workflow_concurrency_limit"}, ReopenedSteps: []string{"finish"}, ReusedSteps: []string{"collect"}})
		default:
			t.Errorf("preview applied work: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "account")
	for _, asJSON := range []bool{false, true} {
		c, err := parseCustomerOperationCommand([]string{"inspect", "op", "--app", "exports"})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if code, err := runCustomerOperationCommand(t.Context(), client, c, &out, asJSON); err != nil || code != 0 {
			t.Fatal("inspection", code, err)
		}
		if !strings.Contains(out.String(), "collect") {
			t.Fatal("inspection omitted completed step")
		}
		c, err = parseCustomerOperationCommand([]string{"recover", "op", "--app", "exports", "--preview", "--expected-generation", "1", "--resolution", "safe_to_retry"})
		if err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if code, err := runCustomerOperationCommand(t.Context(), client, c, &out, asJSON); err != nil || code != 4 {
			t.Fatal("blocked preview exit", code, err)
		}
		if asJSON && !json.Valid(out.Bytes()) {
			t.Fatal("invalid preview JSON")
		}
		if !asJSON && !strings.Contains(out.String(), "external effects") {
			t.Fatal("preview implied evidence of safe retry")
		}
	}
	if calls != 4 {
		t.Fatal("extra requests", calls)
	}
}

func TestCustomerOperationPreviewFlagsRejectAmbiguousDecisions(t *testing.T) {
	base := []string{"recover", "op", "--app", "exports", "--preview", "--expected-generation", "1", "--resolution", "failed"}
	for _, extra := range [][]string{{"--recovery-id", "decision"}, {"--evidence-file", "private"}, {"--inspection-revision", "revision"}, {"--result-file", "result"}, {"--self"}} {
		if _, err := parseCustomerOperationCommand(append(append([]string{}, base...), extra...)); err == nil {
			t.Fatal("ambiguous preview accepted", extra)
		}
	}
	if _, err := parseCustomerOperationCommand([]string{"inspect", "op", "--self"}); err == nil {
		t.Fatal("tenant inspection accepted")
	}
	if _, err := parseCustomerOperationCommand([]string{"recover", "op", "--app", "exports", "--preview", "--resolution", "failed"}); err == nil {
		t.Fatal("unfenced preview accepted")
	}
}
