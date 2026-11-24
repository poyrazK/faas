package main_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

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

type parkingHealthClient struct {
	polls int
}

func (c *parkingHealthClient) GetAppHealth(context.Context, string) (api.AppHealthResponse, error) {
	c.polls++
	return api.AppHealthResponse{Checks: []api.AppHealthCheck{{Code: "readiness", Status: "fail", Detail: "0 of 1 required replicas are ready"}}}, nil
}

func (c *parkingHealthClient) GetApp(context.Context, string) (api.AppResponse, error) {
	app := api.AppResponse{Slug: "worker", Status: "active", WorkloadClass: api.ExecutionModeWorker}
	if c.polls >= 2 {
		app.Status = "evicted_cold"
		app.ParkedDeployment = &api.ParkedDeploymentRef{ID: "d1", ParkedReason: "liveness_exhausted"}
	}
	return app, nil
}

// TestRestartWaitStopsWhenTheAppIsParked pins the agent eval of 2026-11-23
// (voting-app P9): `app restart --project --fresh --wait` waited the whole
// timeout, silently, on a worker that crash-looped into a park.
func TestRestartWaitStopsWhenTheAppIsParked(t *testing.T) {
	var stderr bytes.Buffer
	previous := *main.OsStderr
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = previous })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := &parkingHealthClient{}
	err := main.WaitAppReadiness(ctx, client, "worker", time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "parked: crash-looped") || !strings.Contains(err.Error(), "gregale logs worker") {
		t.Fatalf("err = %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the wait ran to its deadline")
	}
	if got := strings.Count(stderr.String(), "Waiting for worker to become ready: 0 of 1 required replicas are ready"); got != 1 {
		t.Fatalf("progress printed %d times:\n%s", got, stderr.String())
	}
}

// TestSecretFailureWithoutInstancePointsAtStartup pins the agent eval of
// 2026-11-23 (voting-app P10): env list said "delivery failed" for secrets
// whose only deliveries went to boots that crashed, and sent the agent to
// review operations instead of the app's startup.
func TestSecretFailureWithoutInstancePointsAtStartup(t *testing.T) {
	running := []api.SecretRuntimeReloadObservation{{InstanceID: "i1", RuntimeState: "running"}}
	if got := main.SecretDeliveryRowLabel("failed", true, nil); !strings.Contains(got, "boot that did not start") {
		t.Errorf("no-instance failure label = %q", got)
	}
	for _, tc := range []struct {
		complete bool
		targets  []api.SecretRuntimeReloadObservation
	}{{complete: true, targets: running}, {complete: false}} {
		if got := main.SecretDeliveryRowLabel("failed", tc.complete, tc.targets); got != "delivery failed" {
			t.Errorf("complete=%v targets=%d label = %q", tc.complete, len(tc.targets), got)
		}
	}

	var out bytes.Buffer
	previous := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = previous })
	main.PrintSecretDeliveryOverview("worker", "", &api.AppSecretListResponse{Secrets: []api.AppSecretResponse{
		{Key: "DATABASE_URL", DeliveryStatus: "failed", DeliveryVersion: 1, RuntimeReloadTargetsComplete: true},
		{Key: "REDIS_URL", DeliveryStatus: "failed", DeliveryVersion: 1, RuntimeReloadTargetsComplete: true},
	}})
	if got := out.String(); !strings.Contains(got, "boots that did not start") || !strings.Contains(got, "logs worker") ||
		strings.Contains(got, "Review current operations") {
		t.Fatalf("overview:\n%s", got)
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
