package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
)

var doctorTestNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func doctorAppFixtures(now time.Time) (api.AppResponse, doctorAppInputs) {
	app := api.AppResponse{ID: inspectAppID, Slug: "my-api", Status: "active", RAMMB: 128, DeploymentAvailability: api.AppDeploymentAvailabilityLive}
	peak := 50
	in := doctorAppInputs{
		Deployments: api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: inspectDepID, AppID: app.ID, Status: "live", TrafficPercent: 100, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)}}},
		Instances:   []api.InstanceResponse{{ID: "instance-one", AppID: app.ID, DeploymentID: inspectDepID}},
		Events:      api.ListAuditEventsResponse{Limit: 100, Events: []api.AuditEventResponse{doctorEvent(events.WakeReadiness200, now.Add(-time.Minute), map[string]any{"app_id": app.ID, "instance_id": "instance-one"})}},
		Analytics:   api.RequestAnalyticsResponse{Slug: app.Slug, From: now.Add(-doctorAppEvidenceWindow).Format(time.RFC3339), Until: now.Format(time.RFC3339), AsOf: now.Format(time.RFC3339), Routes: []api.RequestAnalyticsRoute{{Requests: 2, GuestPeakRSSMaxMB: &peak, DeploymentObservations: []api.RequestAnalyticsRouteDeploymentObservation{{DeploymentID: inspectDepID, Requests: 2}}}}},
	}
	return app, in
}

func doctorEvent(kind string, at time.Time, data map[string]any) api.AuditEventResponse {
	raw, _ := json.Marshal(data)
	return api.AuditEventResponse{Kind: kind, At: at.Format(time.RFC3339Nano), Data: raw}
}

func doctorFindCheck(t *testing.T, report doctorReport, name string) doctorCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %s missing: %+v", name, report)
	return doctorCheck{}
}

func TestDoctorAppReportEvidence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mutate        func(*api.AppResponse, *doctorAppInputs)
		check, status string
		wantExit      int
	}{
		{"healthy observations", func(_ *api.AppResponse, _ *doctorAppInputs) {}, "deployment", "ok", 0},
		{"memory pressure", func(_ *api.AppResponse, in *doctorAppInputs) { *in.Analytics.Routes[0].GuestPeakRSSMaxMB = 120 }, "runtime-memory", "warn", 0},
		{"missing memory", func(_ *api.AppResponse, in *doctorAppInputs) { in.Analytics.Routes[0].GuestPeakRSSMaxMB = nil }, "runtime-memory", "unknown", 3},
		{"missing route measurement", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Analytics.Routes = append(in.Analytics.Routes, api.RequestAnalyticsRoute{Requests: 1})
		}, "runtime-memory", "unknown", 3},
		{"mixed revisions", func(_ *api.AppResponse, in *doctorAppInputs) { in.Analytics.Routes[0].OtherDeploymentRequests = 1 }, "runtime-memory", "unknown", 3},
		{"unknown revision", func(_ *api.AppResponse, in *doctorAppInputs) { in.Analytics.Routes[0].DeploymentObservations = nil }, "runtime-memory", "unknown", 3},
		{"foreign memory", func(_ *api.AppResponse, in *doctorAppInputs) { in.Analytics.Slug = "other-app" }, "runtime-memory", "unknown", 3},
		{"stale memory", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Analytics.Until = doctorTestNow.Add(-time.Hour).Format(time.RFC3339)
		}, "runtime-memory", "unknown", 3},
		{"future memory", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Analytics.AsOf = doctorTestNow.Add(time.Hour).Format(time.RFC3339)
		}, "runtime-memory", "unknown", 3},
		{"wide window", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Analytics.From = doctorTestNow.Add(-time.Hour).Format(time.RFC3339)
		}, "runtime-memory", "unknown", 3},
		{"truncated routes", func(_ *api.AppResponse, in *doctorAppInputs) { in.Analytics.RoutesTruncated = true }, "runtime-memory", "unknown", 3},
		{"plan gated analytics", func(_ *api.AppResponse, in *doctorAppInputs) { in.AnalyticsErr = errors.New("private provider body") }, "runtime-memory", "unknown", 3},
		{"quiet app", func(_ *api.AppResponse, in *doctorAppInputs) { in.Events.Events = nil }, "startup-timeout", "unknown", 3},
		{"stale wake", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Events.Events[0].At = doctorTestNow.Add(-time.Hour).Format(time.RFC3339)
		}, "startup-timeout", "unknown", 3},
		{"foreign wake", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Events.Events[0].Data = json.RawMessage(`{"app_id":"other","instance_id":"instance-one"}`)
		}, "startup-timeout", "unknown", 3},
		{"foreign instance", func(_ *api.AppResponse, in *doctorAppInputs) { in.Instances[0].AppID = "other" }, "startup-timeout", "unknown", 3},
		{"foreign deployment", func(_ *api.AppResponse, in *doctorAppInputs) { in.Deployments.Items[0].AppID = "other" }, "deployment", "unknown", 3},
		{"old deployment wake", func(_ *api.AppResponse, in *doctorAppInputs) { in.Instances[0].DeploymentID = "superseded" }, "startup-timeout", "unknown", 3},
		{"malformed wake", func(_ *api.AppResponse, in *doctorAppInputs) { in.Events.Events[0].Data = json.RawMessage(`{`) }, "startup-timeout", "unknown", 3},
		{"truncated events", func(_ *api.AppResponse, in *doctorAppInputs) { in.Events.Limit = 1 }, "runtime-oom", "unknown", 3},
		{"missing deployments", func(_ *api.AppResponse, in *doctorAppInputs) { in.DeploymentErr = errors.New("private") }, "deployment", "unknown", 3},
		{"no assigned live deployment", func(app *api.AppResponse, _ *doctorAppInputs) {
			app.DeploymentAvailability = api.AppDeploymentAvailabilityMissing
		}, "deployment", "error", 1},
		{"serving outside history", func(_ *api.AppResponse, in *doctorAppInputs) {
			in.Deployments.Items[0].Status = "failed"
			in.Deployments.Items[0].TrafficPercent = 0
		}, "deployment", "unknown", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, in := doctorAppFixtures(doctorTestNow)
			tc.mutate(&app, &in)
			report := buildDoctorAppReport(app, in, doctorTestNow)
			if got := doctorFindCheck(t, report, tc.check); got.Status != tc.status {
				t.Fatalf("%s = %+v, want %s", tc.check, got, tc.status)
			}
			if got := doctorAppExitCode(report, false); got != tc.wantExit {
				t.Fatalf("exit = %d, want %d", got, tc.wantExit)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "private") {
				t.Fatalf("report leaked raw failure: %s", encoded)
			}
		})
	}
}

func TestDoctorAppCurrentFailureAndCandidate(t *testing.T) {
	for _, serving := range []bool{false, true} {
		app, in := doctorAppFixtures(doctorTestNow)
		failed := api.DeploymentResponse{ID: "failed-candidate", AppID: app.ID, Status: "failed", ErrorCode: api.CodeAppStartupTimeout, ErrorWhy: "private secret", CreatedAt: doctorTestNow.Add(-time.Hour).Format(time.RFC3339)}
		status, exit := "error", 1
		if serving {
			in.Deployments.Items = append([]api.DeploymentResponse{failed}, in.Deployments.Items...)
			status, exit = "warn", 0
		} else {
			app.DeploymentAvailability = api.AppDeploymentAvailabilityMissing
			in.Deployments.Items = []api.DeploymentResponse{failed}
		}
		report := buildDoctorAppReport(app, in, doctorTestNow)
		for _, name := range []string{"deployment", "startup-timeout"} {
			c := doctorFindCheck(t, report, name)
			if c.Status != status || c.DeploymentID != failed.ID || c.Code != failed.ErrorCode || c.ObservedAt != "" || c.DeploymentCreatedAt != failed.CreatedAt {
				t.Fatalf("serving=%v %s = %+v", serving, name, c)
			}
		}
		if serving && doctorAppExitCode(report, false) != exit {
			t.Fatal("failed candidate incorrectly failed serving app")
		}
		if doctorAppExitCode(report, true) != 1 {
			t.Fatal("strict mode did not fail finding")
		}
	}
}

func TestDoctorAppLifecycleFailures(t *testing.T) {
	app, in := doctorAppFixtures(doctorTestNow)
	in.Events.Events = append(in.Events.Events, doctorEvent(events.WakeBootFailed, doctorTestNow.Add(-30*time.Second), map[string]any{"app_id": app.ID, "instance_id": "instance-one", "reason": "private password"}))
	report := buildDoctorAppReport(app, in, doctorTestNow)
	if c := doctorFindCheck(t, report, "startup-timeout"); c.Status != "error" || c.Code != "startup_failed" {
		t.Fatalf("later failure masked: %+v", c)
	}
	if doctorAppExitCode(report, false) != 1 {
		t.Fatal("confirmed failure must fail")
	}
	in.Events.Events = append(in.Events.Events, doctorEvent(events.WakeReadiness200, doctorTestNow.Add(-10*time.Second), map[string]any{"app_id": app.ID, "instance_id": "instance-one"}))
	if c := doctorFindCheck(t, buildDoctorAppReport(app, in, doctorTestNow), "startup-timeout"); c.Status != "ok" {
		t.Fatalf("recovery masked: %+v", c)
	}
	in.Events.Events = append(in.Events.Events, doctorEvent(events.InstanceWorkloadOOMFailed, doctorTestNow.Add(-5*time.Second), map[string]any{"app_id": app.ID, "deployment_id": inspectDepID, "provider_token": "private"}))
	c := doctorFindCheck(t, buildDoctorAppReport(app, in, doctorTestNow), "runtime-oom")
	if c.Status != "error" || c.Code != api.CodeAppRuntimeOOM || c.ObservedAt == "" || c.DeploymentCreatedAt != "" {
		t.Fatalf("OOM evidence lost: %+v", c)
	}
	encoded, _ := json.Marshal(c)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("raw event payload leaked")
	}
}

func TestDoctorAppCommandReadsAndOutput(t *testing.T) {
	app, in := doctorAppFixtures(time.Now().UTC().Truncate(time.Second))
	var mu sync.Mutex
	seen := make(map[string]bool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fp_live_x" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		mu.Lock()
		seen[r.URL.Path] = true
		mu.Unlock()
		var body any
		switch r.URL.Path {
		case "/v1/apps/my-api":
			body = app
		case "/v1/apps/my-api/deployments":
			if r.URL.Query().Get("limit") != "100" {
				t.Error("deployment read is unbounded")
			}
			body = in.Deployments
		case "/v1/apps/my-api/instances":
			if r.URL.Query().Get("history") != "true" {
				t.Error("missing lifecycle history")
			}
			body = in.Instances
		case "/v1/audit-events":
			if r.URL.Query().Get("app_id") != app.ID || r.URL.Query().Get("include_anonymous") != "true" || r.URL.Query().Get("limit") != "100" {
				t.Error("unscoped lifecycle read")
			}
			body = in.Events
		case "/v1/apps/my-api/analytics":
			from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
			until, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("until"))
			if err1 != nil || err2 != nil || until.Sub(from) != doctorAppEvidenceWindow {
				t.Error("unbounded analytics window")
			}
			body = in.Analytics
		default:
			t.Errorf("unexpected endpoint %s", r.URL)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	configureInspectSummaryTest(t, srv.URL)
	stdout, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"doctor", "--app", app.Slug, "--json"}); code != 0 {
		t.Fatalf("exit=%d, stderr=%s, body=%s", code, stderr(), stdout)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.App == nil || report.App.Slug != app.Slug || report.App.CheckedAt == "" || len(report.Checks) != 4 || report.Path != "" || report.Image != nil {
		t.Fatalf("wrong report: %+v", report)
	}
	mu.Lock()
	count := len(seen)
	mu.Unlock()
	if count != 5 {
		t.Fatalf("expected five existing API reads, got %d", count)
	}
	var human bytes.Buffer
	renderDoctorAppHuman(&human, report)
	for _, want := range []string{"app my-api", "Checked at:", "runtime-memory", "window:", "50 MiB", "evidence recorded:"} {
		if !strings.Contains(human.String(), want) {
			t.Errorf("human output missing %q: %s", want, human.String())
		}
	}
}

func TestDoctorAppCommandValidation(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"--app", ""}, {"--app", "../bad"}, {"--app", "my-api", "."},
		{"--app", "my-api", "--image", "example.com/app"}, {"--app", "", "--image", "example.com/app"},
		{"--app", "my-api", "--registry-user", "user"},
	} {
		if got := cmdDoctor(args); got != 2 {
			t.Errorf("%v exit=%d, want 2", args, got)
		}
	}
}

func TestDoctorAppExitPrecedence(t *testing.T) {
	for _, tc := range []struct {
		statuses []string
		strict   bool
		want     int
	}{
		{[]string{"ok"}, false, 0}, {[]string{"warn"}, false, 0}, {[]string{"warn"}, true, 1},
		{[]string{"unknown"}, false, 3}, {[]string{"unknown"}, true, 3},
		{[]string{"unknown", "error"}, false, 1}, {[]string{"unknown", "warn"}, true, 1},
	} {
		report := doctorReport{}
		for _, status := range tc.statuses {
			report.Checks = append(report.Checks, doctorCheck{Status: status})
		}
		if got := doctorAppExitCode(report, tc.strict); got != tc.want {
			t.Fatalf("%v strict=%v: %d", tc.statuses, tc.strict, got)
		}
	}
}
