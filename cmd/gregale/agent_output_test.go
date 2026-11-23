package main_test

import (
	"bytes"
	"strings"
	"testing"

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
	main.RenderDoctorDeploy(&out, rep)
	got := out.String()
	for _, hidden := range []string{"port-bind", "arch", "runtime-oom", "requires deployed-app RAM telemetry"} {
		if strings.Contains(got, hidden) {
			t.Errorf("deploy doctor lists %q:\n%s", hidden, got)
		}
	}
	for _, want := range []string{"env-required", "DATABASE_URL is read but not set", "2 other check(s) passed, 2 skipped until the app is deployed; `gregale doctor` lists every check"} {
		if !strings.Contains(got, want) {
			t.Errorf("deploy doctor lacks %q:\n%s", want, got)
		}
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
			if strings.Contains(got, "inspect my-api") != tc.next {
				t.Fatalf("overview = %q; want next command shown = %v", got, tc.next)
			}
		})
	}
}
