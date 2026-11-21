package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// boa multi-service deploy (2026-11-21): a canary at 1% and the broken v1 at
// 99% both listed as plain "live", so neither the list nor project info said
// which release served.
func TestListedReleaseStatusNamesPartialTraffic(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    api.DeploymentResponse
		want string
	}{
		{"sole live release", api.DeploymentResponse{Status: "live", TrafficPercent: 100}, "live"},
		{"canary", api.DeploymentResponse{Status: "live", TrafficPercent: 1, RolloutState: "rolling_out"}, "live/1%"},
		{"replaced baseline", api.DeploymentResponse{Status: "live", TrafficPercent: 99}, "live/99%"},
		{"live without a recorded share", api.DeploymentResponse{Status: "live"}, "live"},
		{"aborted canary", api.DeploymentResponse{Status: "live", RolloutState: api.DebugRolloutAborted}, "aborted"},
		{"failed", api.DeploymentResponse{Status: "failed", TrafficPercent: 0}, "failed"},
	} {
		if got := listedReleaseStatus(tc.d); got != tc.want {
			t.Errorf("%s: listedReleaseStatus = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := parkedReleaseStatus("live/1%"); got != "live/1%/parked" {
		t.Errorf("parked canary = %q", got)
	}
	if got := parkedReleaseStatus("live"); got != "live/parked" {
		t.Errorf("parked live = %q", got)
	}
	if got := parkedReleaseStatus("failed"); got != "failed" {
		t.Errorf("parked failed = %q", got)
	}
}

func TestProjectInfoNamesPartialTraffic(t *testing.T) {
	canary := api.ProjectWorkloadResponse{Slug: "boa-contacts", Status: "active", DeploymentStatus: "live", TrafficPercent: 1}
	if got := projectWorkloadDeploymentStatus(canary, nil); got != "live/1%" {
		t.Errorf("canary cell = %q, want live/1%%", got)
	}
	full := api.ProjectWorkloadResponse{Slug: "boa-ledger", Status: "active", DeploymentStatus: "live", TrafficPercent: 100}
	if got := projectWorkloadDeploymentStatus(full, nil); got != "live" {
		t.Errorf("full cell = %q, want live", got)
	}
}

// A 6-of-8 project deploy printed only `release resume`, which reconnects
// and never rebuilds; the retry command was only in --help --all.
func TestProjectDeployReceiptNamesRetryForFailedWorkloads(t *testing.T) {
	session := &savedDeploySession{record: deploySessionRecord{ID: "sess-123", Project: &savedProjectDeploy{Slug: "boa"}}}
	apply := api.ApplyResponse{
		ProjectID: "p1",
		Apps:      []api.ApplyResponseApp{{Slug: "boa-frontend"}, {Slug: "boa-contacts"}},
		Builds: []api.AppliedBuild{
			{Slug: "boa-frontend", DeploymentID: "d1", BuildID: "b1"},
			{Slug: "boa-contacts", Error: "scheduler_timeout"},
		},
	}
	for _, asJSON := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		previousOut, previousErr, previousJSON := osStdout, osStderr, jsonOutput
		osStdout, osStderr, jsonOutput = &stdout, &stderr, asJSON
		code := renderProjectDeployReceipt(api.PlanResponse{}, apply, session, false, nil)
		osStdout, osStderr, jsonOutput = previousOut, previousErr, previousJSON
		if code != 1 {
			t.Fatalf("json=%t: exit = %d, want 1", asJSON, code)
		}
		want := "release attempt retry sess-123 --yes"
		all := stdout.String() + stderr.String()
		if !strings.Contains(all, want) {
			t.Fatalf("json=%t: output lacks %q:\n%s", asJSON, want, all)
		}
		if asJSON && !strings.Contains(stdout.String(), `"retry_command"`) {
			t.Fatalf("json receipt lacks retry_command:\n%s", stdout.String())
		}
	}

	// Nothing failed: no retry command.
	var stdout bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &stdout, false
	defer func() { osStdout, jsonOutput = previousOut, previousJSON }()
	ok := apply
	ok.Builds = ok.Builds[:1]
	ok.Apps = ok.Apps[:1]
	renderProjectDeployReceipt(api.PlanResponse{}, ok, session, false, nil)
	if strings.Contains(stdout.String(), "attempt retry") {
		t.Fatalf("successful deploy names retry:\n%s", stdout.String())
	}
}

func TestRolloutsRecoverExpectedStepOnlyForAdvanceAndPromote(t *testing.T) {
	var stderr bytes.Buffer
	previousErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = previousErr }()
	if code := cmdRolloutsRecover([]string{"boa-contacts", "--action", "abort", "--expected-step", "1"}); code == 0 {
		t.Fatal("abort with --expected-step succeeded")
	}
	if !strings.Contains(stderr.String(), "--expected-step applies only") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestDeployCompactHelpShowsProjectFlags(t *testing.T) {
	var out bytes.Buffer
	if !printManifestHelp(&out, cliCommandByName(t, "deploy"), nil) {
		t.Fatal("deploy help not rendered")
	}
	help := out.String()
	for _, want := range []string{"--project", "--only SLUGS", "--rollout MODE", "release attempt retry SESSION_ID"} {
		if !strings.Contains(help, want) {
			t.Errorf("compact deploy help lacks %q", want)
		}
	}
	if strings.Contains(help, "--canary-stages") {
		t.Error("compact deploy help lists a deprecated flag")
	}
}

func cliCommandByName(t *testing.T, name string) cliCommand {
	t.Helper()
	for _, command := range cliCommands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("no %s command", name)
	return cliCommand{}
}
