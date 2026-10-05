package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
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
