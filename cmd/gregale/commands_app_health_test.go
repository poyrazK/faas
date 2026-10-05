package main

import (
	"net/http"
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

func TestAppHealth_CLIRejectsExtraArgs(t *testing.T) {
	resetJSONOut(t)
	if code := cmdAppHealth("demo", []string{"unexpected"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
}
