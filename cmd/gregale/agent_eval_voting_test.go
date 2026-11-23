package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestWorkerLivenessParkSaysCrashLooped pins the agent eval of 2026-11-23
// (voting-app P3): "parked: liveness failed" on a worker sent the agent to
// add a /healthz server, though workers get no HTTP probe and only crashes
// park them. A Compose worker (workload class) reads the same as a declared one.
func TestWorkerLivenessParkSaysCrashLooped(t *testing.T) {
	parked := func() api.AppResponse {
		return api.AppResponse{ID: "a1", Slug: "worker", Status: "evicted_cold",
			ParkedDeployment: &api.ParkedDeploymentRef{ID: "d1", ParkedReason: "liveness_exhausted"}}
	}
	declared, compose := parked(), parked()
	declared.Manifest.ExecutionMode = api.ExecutionModeWorker
	compose.WorkloadClass = api.ExecutionModeWorker
	for _, app := range []api.AppResponse{declared, compose} {
		if got := main.AppParkedLabel(app); got != "parked: crash-looped" {
			t.Errorf("worker label = %q", got)
		}
		var out bytes.Buffer
		main.WriteParkedNotice(&out, app)
		if !strings.Contains(out.String(), "process kept exiting") || strings.Contains(out.String(), "liveness checks") {
			t.Errorf("worker notice = %q", out.String())
		}
	}
	for _, app := range []main.InspectAppSummary{
		{Slug: "worker", Status: "evicted_cold", ParkedReason: "liveness_exhausted", ExecutionMode: api.ExecutionModeWorker},
		{Slug: "worker", Status: "evicted_cold", ParkedReason: "liveness_exhausted", WorkloadClass: api.ExecutionModeWorker},
	} {
		recs := main.InspectRecommendations(main.InspectSummary{App: app})
		if len(recs) == 0 || !strings.Contains(recs[0].Message, "process kept exiting") {
			t.Errorf("inspect %+v recommendations = %+v", app, recs)
		}
	}
}

// TestStartupCrashCauseHintNamesMigrationCredential pins the agent eval of
// 2026-11-23 (voting-app P6): a worker creating its table with the serving
// DATABASE_URL died on 42501 and the deploy gave only generic crash advice.
func TestStartupCrashCauseHintNamesMigrationCredential(t *testing.T) {
	npgsql := "workload stopped during startup (guest 7d70 exited before becoming ready): " +
		"Npgsql.PostgresException (0x80004005): 42501: permission denied for schema public\n  SqlState: 42501"
	for _, tc := range []struct {
		name, code, detail string
		want               bool
	}{
		{name: "create in schema", code: api.CodeAppStartupCrash, detail: npgsql, want: true},
		{name: "alter a table", code: api.CodeAppStartupCrash, detail: "psycopg.errors.InsufficientPrivilege: must be owner of table votes", want: true},
		{name: "missing DML grant", code: api.CodeAppStartupCrash, detail: "42501: permission denied for table votes"},
		{name: "other crash", code: api.CodeAppStartupCrash, detail: "Error: PORT is not set"},
		{name: "not a startup crash", code: api.CodeAppStartupTimeout, detail: npgsql},
	} {
		hint := main.StartupCrashCauseHint(tc.code, tc.detail)
		if got := strings.Contains(hint, "MIGRATION_DATABASE_URL") && strings.Contains(hint, "release.command"); got != tc.want {
			t.Errorf("%s: hint = %q, want hint=%v", tc.name, hint, tc.want)
		}
	}
}
