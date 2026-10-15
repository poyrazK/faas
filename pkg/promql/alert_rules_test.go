package promql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertRulesEnvelopeAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		bad        bool
	}{
		{"empty", `{"status":"success","data":{"groups":[]}}`, 200, false},
		{"missing", `{"status":"success","data":{}}`, 200, true},
		{"error", `{"status":"error","data":{"groups":[]}}`, 200, true},
		{"warning", `{"status":"success","warnings":["partial"],"data":{"groups":[]}}`, 200, true},
		{"info", `{"status":"success","infos":["partial"],"data":{"groups":[]}}`, 200, true},
		{"missing rules", `{"status":"success","data":{"groups":[{"name":"x"}]}}`, 200, true},
		{"trailing JSON", `{"status":"success","data":{"groups":[]}} {}`, 200, true},
		{"oversized", `{"status":"success","data":{"groups":[]}}` + strings.Repeat(" ", promqlResponseMaxBytes), 200, true},
		{"HTTP", `secret`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/rules" || r.URL.Query().Get("type") != "alert" {
					t.Errorf("unexpected request %s", r.URL.String())
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewClient(srv.URL, srv.Client()).AlertRules(context.Background())
			if (err != nil) != tc.bad {
				t.Fatalf("err=%v", err)
			}
		})
	}
	var client *Client
	if _, err := client.AlertRules(context.Background()); err == nil {
		t.Fatal("nil client accepted")
	}
	for _, kind := range []string{"rules", "alerts", "groups"} {
		t.Run(kind, func(t *testing.T) {
			groups := []AlertRuleGroup{{Rules: []AlertRule{}}}
			switch kind {
			case "rules":
				groups[0].Rules = make([]AlertRule, promqlMaxSeries+1)
			case "alerts":
				groups[0].Rules = []AlertRule{{Alerts: make([]Alert, promqlMaxSeries+1)}}
			case "groups":
				groups = make([]AlertRuleGroup, promqlMaxSeries+1)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": groups}})
			}))
			defer srv.Close()
			if _, err := NewClient(srv.URL, srv.Client()).AlertRules(context.Background()); err == nil {
				t.Fatal("oversized rule snapshot accepted")
			}
		})
	}
}
