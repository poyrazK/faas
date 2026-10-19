package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstanceUnknownParkStopsPolling(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			previousJSON := jsonOutput
			jsonOutput = format == "json"
			t.Cleanup(func() { jsonOutput = previousJSON })
			posts, polls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					posts++
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"ok":true,"intent_id":"11111111-1111-1111-1111-111111111111","status_url":"/v1/admin/operator-intents/11111111-1111-1111-1111-111111111111"}`))
					return
				}
				polls++
				_, _ = w.Write([]byte(`{"intent_id":"11111111-1111-1111-1111-111111111111","status":"outcome_unknown","error":"teardown acknowledgement unavailable","park_execution":{"phase":"outcome_unknown","generation":"original-vm"}}`))
			}))
			defer server.Close()
			installTestOperatorSession(t, server.URL, "opaque-session")
			stdout, stderr, restore := captureOperatorIO()
			defer restore()
			jsonOutput = format == "json"
			code := cmdInstancesForcePark([]string{"--instance-id=33333333-3333-3333-3333-333333333333", "--yes", "--timeout=1s"})
			if code != 1 || posts != 1 || polls != 1 {
				t.Fatalf("exit=%d posts=%d polls=%d", code, posts, polls)
			}
			if format == "json" {
				var result struct {
					Outcome struct {
						Status    string `json:"status"`
						Execution struct {
							Generation string `json:"generation"`
						} `json:"park_execution"`
					} `json:"outcome"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Outcome.Status != "outcome_unknown" || result.Outcome.Execution.Generation != "original-vm" {
					t.Fatalf("missing recovery evidence: %s", stdout.String())
				}
			} else if !strings.Contains(stderr.String(), "outcome unknown") || !strings.Contains(stderr.String(), "automatic redispatch stopped") {
				t.Fatalf("missing reconciliation guidance: %s", stderr.String())
			}
		})
	}
}
