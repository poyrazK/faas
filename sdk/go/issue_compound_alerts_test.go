// adr: 1662
package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueCompoundAlertClient(t *testing.T) {
	rule := faas.IssueCompoundAlertRule{Key: "payments", Enabled: true, Environment: "production", MinimumEvents: 20, MinimumCustomers: 3, WindowSeconds: 600, CooldownSeconds: 1800}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			var in faas.UpdateIssueCompoundAlertPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision != 2 || len(in.Rules) != 1 || in.Rules[0] != rule {
				t.Errorf("policy=%+v err=%v", in, err)
			}
			_ = json.NewEncoder(w).Encode(faas.IssueCompoundAlertPolicy{Revision: 3, Rules: []faas.IssueCompoundAlertRule{rule}})
		case http.MethodPost:
			var in faas.PreviewIssueCompoundAlertRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Rule != rule || in.IssueID != "one" {
				t.Errorf("preview=%+v err=%v", in, err)
			}
			_, _ = w.Write([]byte(`{"allowed":false,"events_matched":true,"customers_matched":false,"reason":"customer_threshold"}`))
		default:
			_ = json.NewEncoder(w).Encode(faas.IssueCompoundAlertPolicy{Revision: 3, Rules: []faas.IssueCompoundAlertRule{rule}})
		}
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := c.GetIssueCompoundAlertPolicy(context.Background(), "payments"); err != nil || len(out.Rules) != 1 {
		t.Fatal(out, err)
	}
	revision := int64(2)
	if out, err := c.SetIssueCompoundAlertPolicy(context.Background(), "payments", faas.UpdateIssueCompoundAlertPolicyRequest{ExpectedRevision: &revision, Rules: []faas.IssueCompoundAlertRule{rule}}); err != nil || out.Revision != 3 {
		t.Fatal(out, err)
	}
	if out, err := c.PreviewIssueCompoundAlert(context.Background(), "payments", faas.PreviewIssueCompoundAlertRequest{IssueID: "one", Rule: rule}); err != nil || out.Allowed || out.CustomersMatched || !out.EventsMatched {
		t.Fatal(out, err)
	}
	if requests != 3 {
		t.Fatal(requests)
	}
}
