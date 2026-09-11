package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const testBuildSweepTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestBuildsSweepStuckUsesAuthenticatedAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/admin/builds/sweep-stuck" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("older_than") != "20m0s" || r.URL.Query().Get("reason") != "builder_vm_timeout" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing Idempotency-Key")
		}
		if got := r.Header.Get(operatorTraceIDHeader); got != testBuildSweepTraceID {
			t.Errorf("trace id = %q, want %q", got, testBuildSweepTraceID)
		}
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		writeTestJSON(w, http.StatusOK, api.SweepStuckBuildsResponse{
			OK: true, SweptCount: 3, OlderThanSecs: 1200, ThresholdISO: "2026-09-11T12:00:00Z",
		})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()
	code := cmdBuildsDispatch([]string{"sweep-stuck", "--older-than", "20m", "--reason", "builder_vm_timeout", "--trace-id", testBuildSweepTraceID, "--yes"})
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(out.String(), "swept=3") || !strings.Contains(out.String(), "trace_id="+testBuildSweepTraceID) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestBuildsSweepStuckRequiresSafetyInputs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "reason", args: []string{"sweep-stuck", "--yes"}, want: "--reason is required"},
		{name: "confirmation", args: []string{"sweep-stuck", "--reason", "incident_123"}, want: "--yes required"},
		{name: "duration", args: []string{"sweep-stuck", "--reason", "incident_123", "--older-than", "30s", "--yes"}, want: "between 1m and 1h"},
		{name: "trace", args: []string{"sweep-stuck", "--reason", "incident_123", "--trace-id", "invalid", "--yes"}, want: "32 lowercase hex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, restore := captureOperatorIO()
			defer restore()
			if code := cmdBuildsDispatch(tc.args); code != 2 {
				t.Fatalf("exit = %d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestBuildsSweepStuckExplainsStepUp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, http.StatusForbidden, api.Problem{Status: http.StatusForbidden, Code: api.CodeStepUpRequired})
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	_, stderr, restore := captureOperatorIO()
	defer restore()
	code := cmdBuildsDispatch([]string{"sweep-stuck", "--reason", "incident_123", "--yes"})
	if code != 1 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "gregalectl auth step-up") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
