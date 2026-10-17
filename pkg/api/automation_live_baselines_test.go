// adr: 1302
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAutomationLiveBaselineClientEscapedIdentityAndMethods(t *testing.T) {
	calls := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		if r.Method == http.MethodPut {
			var request PromoteAutomationLiveBaselineRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedRevision != 0 || request.SuiteID != "suite" {
				t.Error("lost explicit promotion body")
			}
		} else if r.ContentLength > 0 {
			t.Error("read sent a body")
		}
		if r.URL.Path[len(r.URL.Path)-7:] == "history" {
			_ = json.NewEncoder(w).Encode(AutomationLiveBaselineHistory{Promotions: []AutomationLiveBaseline{}})
		} else {
			_ = json.NewEncoder(w).Encode(AutomationLiveBaseline{PromotionID: "promotion"})
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "token")
	if _, err := c.GetAutomationLiveBaseline(t.Context(), "billing", "case/report", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAutomationLiveBaselineHistory(t.Context(), "billing", "case/report", "production"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PromoteAutomationLiveBaseline(t.Context(), "billing", "case/report", "production", "promotion/id", PromoteAutomationLiveBaselineRequest{SuiteID: "suite"}); err != nil {
		t.Fatal(err)
	}
	root := "/v1/apps/billing/automations/case%2Freport/live-baselines/production"
	want := []string{"GET " + root, "GET " + root + "/history", "PUT " + root + "/promotions/promotion%2Fid"}
	for i, call := range calls {
		if call != want[i] {
			t.Fatalf("request %s, want %s", call, want[i])
		}
	}
}
