// adr: 1304
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListAutomationLiveBaselinesClientIsScopedRead(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-baselines" || r.Header.Get("Authorization") != "Bearer token" || r.ContentLength > 0 {
			t.Error("invalid list request")
		}
		_ = json.NewEncoder(w).Encode(ListAutomationLiveBaselinesResponse{Baselines: []AutomationLiveBaselineHealth{}, ObservedAt: time.Now().UTC()})
	}))
	defer server.Close()
	view, err := NewClient(server.URL, "token").ListAutomationLiveBaselines(t.Context(), "billing", "case/report")
	if err != nil || calls != 1 || view.Baselines == nil || view.ObservedAt.IsZero() {
		t.Fatal("listing", err)
	}
}
