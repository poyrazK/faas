package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppHealth_CLIReadOnlyRoute(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"app_id":"app","status":"unknown","phase":"idle","summary":"Telemetry unavailable","scope":"default","evaluated_at":"2026-10-05T12:00:00Z","valid_for_seconds":120,"serving_deployment_ids":[],"capacity":{"required":0,"ready":0,"starting":0,"unready":0,"unknown":0},"checks":[]}`, http.StatusOK)
	if code := cmdAppDispatch([]string{"demo", "health"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/health" {
		t.Fatalf("%s %s", f.sawMethod, f.sawPath)
	}
}

func TestAppHealth_CLIExplainsTargetsAndPolicy(t *testing.T) {
	resetJSONOut(t)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	authedFakeAPI(t, `{"status":"degraded","checks":[{"code":"readiness","status":"warning","detail":"One replica is unready","findings_truncated":true,"findings":[{"reason":"required_probe_unready","status":"fail","detail":"This required probe reports unready.","deployment_id":"v42","instance_id":"vm-42","source":"sidecar:proxy","observed_at":"2026-10-04T12:00:00Z"}]}],"requests":{"policy":{"minimum_requests":50,"minimum_server_errors":5,"warning_error_rate_pct":5,"unhealthy_error_rate_pct":25}}}`, http.StatusOK)
	if code := cmdAppHealth("demo", nil); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, expected := range []string{"sidecar:proxy", "required_probe_unready", "Deployment: v42; replica: vm-42", "Recorded evidence: 2026-10-04T12:00:00Z", "Additional replica findings omitted", "at least 50 requests and 5 server errors; warning 5.00%, unhealthy 25.00%"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q: %s", expected, output.String())
		}
	}
}

func TestAppHealth_CLIRejectsExtraArgs(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppHealth("demo", []string{"unexpected"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

func TestAppHealth_CLIHistory(t *testing.T) {
	resetJSONOut(t)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	f := authedFakeAPI(t, `{"app_id":"app","scope":"default","collector_fresh":false,"interval_seconds":30,"next_cursor":"12345678-1234-1234-1234-123456789abc","entries":[{"id":"entry","kind":"gap","observed_at":"2026-10-05T12:00:00Z","assessment":{"status":"unknown","phase":"unknown","summary":"Evidence expired"}}]}`, http.StatusOK)
	if code := cmdAppHealth("demo", []string{"--history", "--limit", "5"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/health/history" {
		t.Fatalf("%s %s", f.sawMethod, f.sawPath)
	}
	for _, expected := range []string{"Background collection is unconfirmed", "[gap] unknown", "Evidence expired", "Next page: --history --before", "not exact incident start/end"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q: %s", expected, output.String())
		}
	}
}

// TestAppHealth_CLIEnvironment pins H10-113: a project app's hostname served
// its production environment while `app health` could only assess the
// default scope, whose older release received no traffic, so it always read
// idle. --environment selects the environment, and --check then requires
// that scope rather than "default".
func TestAppHealth_CLIEnvironment(t *testing.T) {
	cases := map[string]struct {
		args     []string
		scope    string
		wantCode int
		wantPath bool
	}{
		"single read":         {args: []string{"--environment", "production"}, scope: "production", wantPath: true},
		"check passes":        {args: []string{"--environment", "production", "--check"}, scope: "production", wantPath: true},
		"check wrong scope":   {args: []string{"--environment", "production", "--check"}, scope: "default", wantCode: 1, wantPath: true},
		"history is rejected": {args: []string{"--environment", "production", "--history"}, wantCode: 1},
		"invalid slug":        {args: []string{"--environment", "Not A Slug"}, wantCode: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resetJSONOut(t)
			var output bytes.Buffer
			previous := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = previous })
			body := `{"app_id":"app","status":"healthy","phase":"serving","summary":"Serving","scope":"` + tc.scope + `","evaluated_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","valid_for_seconds":120,"serving_deployment_ids":["v5"],"checks":[]}`
			f := authedFakeAPI(t, body, http.StatusOK)
			if code := cmdAppHealth("demo", tc.args); code != tc.wantCode {
				t.Fatalf("exit %d, want %d: %s", code, tc.wantCode, output.String())
			}
			sawEnvironment := f.sawPath == "/v1/apps/demo/health" && f.sawQuery == "environment=production"
			if sawEnvironment != tc.wantPath {
				t.Fatalf("request = %s %s?%s", f.sawMethod, f.sawPath, f.sawQuery)
			}
		})
	}
}
