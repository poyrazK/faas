package promql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertsContractAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"metadata", `{"status":"success","data":{"alerts":[{"labels":{"alertname":"FaasApiDown"},"annotations":{"runbook_url":"/docs/runbooks/FaasApiDown.md"},"state":"firing"}]}}`, 200, false},
		{"empty", `{"status":"success","data":{"alerts":[]}}`, 200, false},
		{"missing", `{"status":"success","data":{}}`, 200, true},
		{"error envelope", `{"status":"error","data":{"alerts":[]}}`, 200, true},
		{"invalid JSON", `{`, 200, true},
		{"unavailable", `offline`, 503, true},
		{"oversized", `{"status":"success","data":{"alerts":[]}}` + strings.Repeat(" ", promqlResponseMaxBytes), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/alerts" {
					t.Errorf("path = %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			alerts, err := NewClient(srv.URL, srv.Client()).Alerts(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("Alerts = %v, %v", alerts, err)
			}
			if tc.name == "metadata" && (len(alerts) != 1 || alerts[0].Annotations["runbook_url"] == "") {
				t.Fatalf("missing annotation: %+v", alerts)
			}
		})
	}
}

func TestAlertsSeriesLimitAndDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"alerts": make([]Alert, promqlMaxSeries+1)}})
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL, srv.Client()).Alerts(context.Background()); err == nil {
		t.Fatal("expected alert count limit")
	}
	var client *Client
	if _, err := client.Alerts(context.Background()); err == nil {
		t.Fatal("expected disabled dependency error")
	}
}
