package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Deploy shows findings only and counts the rest on one line; `gregale
// doctor` keeps the per-check list (TestRenderDoctorHuman_HasAllChecks).
func TestDeployDoctorShowsFindingsAndCountsTheRest(t *testing.T) {
	rep := main.DoctorReport{Path: "/src", Checks: []main.DoctorCheck{
		{Name: "port-bind", Status: "ok"},
		{Name: "arch", Status: "ok"},
		{Name: "env-required", Status: "warn", Hint: "DATABASE_URL is read but not set"},
		{Name: "runtime-oom", Status: "skipped", Reason: "requires deployed-app RAM telemetry"},
		{Name: "dep-install", Status: "skipped", Reason: "requires the platform build environment"},
	}}
	var out strings.Builder
	main.RenderDoctorDeploy(&out, rep, false)
	got := out.String()
	for _, hidden := range []string{"port-bind", "arch", "runtime-oom", "requires deployed-app RAM telemetry"} {
		if strings.Contains(got, hidden) {
			t.Errorf("deploy doctor lists %q:\n%s", hidden, got)
		}
	}
	for _, want := range []string{"env-required", "DATABASE_URL is read but not set", "2 other check(s) passed, 2 skipped; `gregale doctor` lists every check"} {
		if !strings.Contains(got, want) {
			t.Errorf("deploy doctor lacks %q:\n%s", want, got)
		}
	}
}

// Under --doctor-strict the deploy already blocks on errors only: the footer
// does not tell the reader to add --strict. With no findings listed, the
// summary does not say "other".
func TestDeployDoctorStrictFooterAndCleanSummary(t *testing.T) {
	warn := main.DoctorReport{Path: "/src", Checks: []main.DoctorCheck{
		{Name: "port-bind", Status: "ok"},
		{Name: "env-required", Status: "warn", Hint: "DATABASE_URL is read but not set"},
	}}
	var out strings.Builder
	main.RenderDoctorDeploy(&out, warn, true)
	if got := out.String(); strings.Contains(got, "run with --strict") || !strings.Contains(got, "--doctor-strict blocks error-class checks only") {
		t.Errorf("strict footer:\n%s", got)
	}

	clean := main.DoctorReport{Path: "/src", Checks: []main.DoctorCheck{
		{Name: "port-bind", Status: "ok"},
		{Name: "runtime-oom", Status: "skipped", Reason: "requires deployed-app RAM telemetry"},
	}}
	out.Reset()
	main.RenderDoctorDeploy(&out, clean, false)
	if got := out.String(); strings.Contains(got, "other") || !strings.Contains(got, "1 check(s) passed, 1 skipped;") {
		t.Errorf("clean summary:\n%s", got)
	}
}

// The delivery overview is one line, with the next command only when there
// is something to look at.
func TestSecretDeliveryOverviewIsOneLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		next   bool
	}{
		{"clean", "delivered", false},
		{"failed", "failed", true},
		{"pending", "pending", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &buf
			defer func() { *main.OsStdout = old }()
			main.PrintSecretDeliveryOverview("my-api", "", &api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{DeliveryStatus: tc.status, DeliveryVersion: 1, DeliveredVersion: 1}}})
			got := buf.String()
			if strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "Delivery (app=my-api") {
				t.Fatalf("overview = %q; want one line", got)
			}
			if strings.Contains(got, "app health my-api --explain") != tc.next {
				t.Fatalf("overview = %q; want next command shown = %v", got, tc.next)
			}
		})
	}
}

// The voting-app worker's failure (2026-11-23 agent eval): the progress
// shows the app's last log lines and a fix that names the app, and the JSON
// event carries runnable commands and the lines.
func TestProjectFailureShowsLogsAndFilledFix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/worker":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "worker", Status: "failed",
				Error: "snapshot prime failed: app_startup_crash: workload stopped during startup", ErrorCode: "app_startup_crash",
				ErrorFix: "• read what it printed (`gregale logs <slug> --since 30m`)\n• check secrets (`gregale secrets list --app <slug>`)"})
		case "/v1/apps/voting-demo-worker/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			if r.URL.Query().Get("deployment") != "" {
				// The Free plan refuses the deployment filter inside a
				// successful stream; the CLI reads the app's lines instead.
				_, _ = io.WriteString(w, "event: error\ndata: {\"code\":\"plan_deployment_filter_not_allowed\"}\n\nevent: end\ndata: {}\n\n")
				return
			}
			_, _ = io.WriteString(w, "event: log\ndata: {\"line\":\"starting worker\",\"stream\":\"stdout\"}\n\n"+
				"event: ping\ndata: {}\n\n"+
				"event: gap\ndata: {\"reason\":\"ring_overflow\"}\n\n"+
				"event: log\ndata: {\"line\":\"permission denied for schema public\",\"stream\":\"stderr\"}\n\n"+
				"event: end\ndata: {}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	apply := func() api.ApplyResponse {
		return api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "voting-demo-worker", DeploymentID: "worker"}}}
	}

	var human bytes.Buffer
	main.WaitForProjectApplyTimeouts(main.WithProjectWaitProgress(t.Context(), &human, false), main.NewClient(srv.URL, "token"), apply(), 20*time.Second)
	got := human.String()
	for _, want := range []string{"last log lines:", "| permission denied for schema public", "`gregale logs voting-demo-worker --since 30m`"} {
		if !strings.Contains(got, want) {
			t.Errorf("progress lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<slug>") {
		t.Errorf("progress still shows <slug>:\n%s", got)
	}

	var machine bytes.Buffer
	main.WaitForProjectApplyTimeouts(main.WithProjectWaitProgress(t.Context(), &machine, true), main.NewClient(srv.URL, "token"), apply(), 20*time.Second)
	var event struct {
		Stage        string           `json:"stage"`
		FixCommands  []string         `json:"fix_commands"`
		RelevantLogs []api.LogExcerpt `json:"relevant_logs"`
	}
	for _, line := range strings.Split(strings.TrimSpace(machine.String()), "\n") {
		if err := json.Unmarshal([]byte(line), &event); err == nil && event.Stage == "failed" {
			break
		}
	}
	if !slices.Equal(event.FixCommands, []string{"gregale logs voting-demo-worker --since 30m", "gregale secrets list --app voting-demo-worker"}) {
		t.Errorf("fix_commands = %q", event.FixCommands)
	}
	if len(event.RelevantLogs) != 2 || event.RelevantLogs[1].Message != "permission denied for schema public" || event.RelevantLogs[1].Source != "stderr" {
		t.Errorf("relevant_logs = %+v", event.RelevantLogs)
	}
}
