package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAuditTraceUsesOperatorSessionAndPrintsTimeline(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	now := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/admin/obs/traces/"+traceID || r.URL.Query().Get("limit") != "25" {
			t.Errorf("request = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		writeTestJSON(w, http.StatusOK, api.ObsTraceLookupResponse{
			TraceID: traceID, GeneratedAt: now, Limit: 25,
			Intents: []api.OperatorIntentResponse{{IntentID: "intent-1", Kind: "force_park", Status: "succeeded", RequestedAt: now}},
			Events:  []api.ObsEventRow{{ID: 42, At: now.Add(time.Second), Actor: "system:schedd", Kind: "operator.action.force_park.outcome"}},
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdAuditDispatch([]string{"trace", "--trace-id", traceID, "--limit", "25"}); code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "intent id=intent-1") || !strings.Contains(out.String(), "event id=42") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestAuditTraceValidatesTraceID(t *testing.T) {
	_, stderr, restore := captureOperatorIO()
	defer restore()
	if code := cmdAuditDispatch([]string{"trace", "--trace-id", "NOT-A-TRACE"}); code != 2 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "must match") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
