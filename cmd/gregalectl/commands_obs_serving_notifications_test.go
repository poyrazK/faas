package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func servingNotificationCLIResponse() api.ObsServingHealth {
	out := servingCLIResponse()
	evaluated, run := out.GeneratedAt.Add(-10*time.Second), *out.Probes[0].LastRunAt
	attempt, success := out.GeneratedAt.Add(-24*time.Hour), out.GeneratedAt.Add(-48*time.Hour)
	on, off, expected := true, false, 2
	out.ProbeNotifications = &api.ObsServingProbeNotificationHealth{
		State: "available", ExpectedChannels: &expected, Limit: api.ObsServingProbeNotificationItemsMax,
		Counts: &api.ObsServingProbeNotificationCounts{LastSubmissionFailed: 1, Disabled: 1},
		Channels: []api.ObsServingProbeNotification{
			{Node: "fsn-2", Channel: "webhook", State: "last_submission_failed", Enabled: &on, EvaluatedAt: &evaluated, LastRunAt: &run, LastAttemptAt: &attempt, LastSuccessAt: &success, FailureCode: "timeout"},
			{Node: "fsn-2", Channel: "heartbeat", State: "disabled", Enabled: &off, EvaluatedAt: &evaluated, LastRunAt: &run},
		},
	}
	return out
}

func TestObsServingProbeNotificationContract(t *testing.T) {
	legacy := servingCLIResponse()
	if !validObsServingHealth(legacy) {
		t.Fatal("an older server became invalid")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*api.ObsServingProbeNotificationHealth)
	}{
		{"private channel", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].Channel = "https://private/token" }},
		{"private node", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].Node = "https://private/token" }},
		{"private code", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].FailureCode = "private-provider-error" }},
		{"private reason", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].Reason = "private-provider-error" }},
		{"missing completion", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].LastAttemptAt = nil }},
		{"epoch success clock", func(d *api.ObsServingProbeNotificationHealth) {
			at := time.Unix(0, 0)
			d.Channels[0].LastSuccessAt = &at
		}},
		{"missing evaluation", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].EvaluatedAt = nil }},
		{"missing run", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].LastRunAt = nil }},
		{"stale evaluation", func(d *api.ObsServingProbeNotificationHealth) {
			at := d.Channels[0].EvaluatedAt.Add(-3 * time.Minute)
			d.Channels[0].EvaluatedAt = &at
		}},
		{"newer success", func(d *api.ObsServingProbeNotificationHealth) {
			at := d.Channels[0].LastAttemptAt.Add(time.Nanosecond)
			d.Channels[0].LastSuccessAt = &at
		}},
		{"false success code", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].FailureCode = "none" }},
		{"probe-only code", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[0].FailureCode = "body_mismatch" }},
		{"fabricated disabled history", func(d *api.ObsServingProbeNotificationHealth) {
			d.Channels[1].LastAttemptAt = d.Channels[0].LastAttemptAt
		}},
		{"unknown fabricated fields", func(d *api.ObsServingProbeNotificationHealth) {
			d.Channels[0].State, d.Channels[0].Reason = "unknown", "recording_unknown"
		}},
		{"changed counts", func(d *api.ObsServingProbeNotificationHealth) { d.Counts.LastSubmissionFailed = 0 }},
		{"changed expected", func(d *api.ObsServingProbeNotificationHealth) { n := 3; d.ExpectedChannels = &n }},
		{"unreviewed limit", func(d *api.ObsServingProbeNotificationHealth) { d.Limit++ }},
		{"false omission", func(d *api.ObsServingProbeNotificationHealth) { d.Omitted = 1 }},
		{"false truncation", func(d *api.ObsServingProbeNotificationHealth) { d.Truncated = true }},
		{"wrong order", func(d *api.ObsServingProbeNotificationHealth) {
			d.Channels[0], d.Channels[1] = d.Channels[1], d.Channels[0]
		}},
		{"duplicate identity", func(d *api.ObsServingProbeNotificationHealth) { d.Channels[1] = d.Channels[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := servingNotificationCLIResponse()
			if !validObsServingHealth(out) {
				t.Fatal("valid submission evidence rejected")
			}
			tc.mutate(out.ProbeNotifications)
			if validObsServingHealth(out) {
				t.Fatal("invalid submission metadata accepted")
			}
		})
	}
}

func TestObsServingProbeNotificationHumanJSONAndRedaction(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	response := servingNotificationCLIResponse()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "fixture")
	for _, args := range [][]string{nil, {"--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsServingHealth(args); code != 0 {
			t.Fatalf("valid notification evidence failed: %d %s", code, stderr)
		}
		if !strings.Contains(stdout.String(), "last_submission_failed") || !strings.Contains(stdout.String(), "timeout") {
			t.Fatal("submission failure was not visible")
		}
	}
	response.ProbeNotifications = nil
	stdout.Reset()
	if cmdObsServingHealth(nil) != 0 || !strings.Contains(stdout.String(), "server_does_not_report_probe_notifications") {
		t.Fatal("older server omission became a known outcome")
	}
	response = servingNotificationCLIResponse()
	response.ProbeNotifications.Channels[0].FailureCode = "https://private-provider/token"
	for _, args := range [][]string{nil, {"--json"}} {
		stdout.Reset()
		stderr.Reset()
		if cmdObsServingHealth(args) != 1 || stdout.Len() != 0 || strings.Contains(stderr.String(), "private-provider") {
			t.Fatal("unsafe optional metadata was exposed")
		}
	}
}

func TestObsDeploymentProbeNotificationEvidenceIsIndependent(t *testing.T) {
	for _, mode := range []string{"failed", "unknown", "malformed", "older server"} {
		t.Run(mode, func(t *testing.T) {
			out := servingNotificationCLIResponse()
			now := out.GeneratedAt
			snapshot := deploymentSnapshotTest(now)
			snapshot.serving.value.ProbeNotifications = out.ProbeNotifications
			switch mode {
			case "unknown":
				snapshot.serving.value.ProbeNotifications = operatorobs.UnavailableServingProbeNotifications("metadata_inconsistent")
			case "malformed":
				snapshot.serving.value.ProbeNotifications.Channels[0].FailureCode = "private-provider-error"
			case "older server":
				snapshot.serving.value.ProbeNotifications = nil
			}
			opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Second, poll: time.Second}
			report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error {
				t.Fatal("submission evidence blocked serving qualification")
				return nil
			}, func(obsDeploymentReport) error { return nil })
			if code != 0 || report.State != "passed" || report.Serving == nil {
				t.Fatal("optional submission history changed qualification")
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "private-provider") {
				t.Fatal("unsafe optional history reached the report")
			}
			if mode == "malformed" && report.Serving.ProbeNotifications.Reason != "metadata_inconsistent" {
				t.Fatal("unsafe metadata was not replaced with unknown evidence")
			}
		})
	}
}
