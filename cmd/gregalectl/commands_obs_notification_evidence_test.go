package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func servingUnknownNotificationCLIResponse(reason string) api.ObsServingHealth {
	out := servingNotificationCLIResponse()
	d := out.ProbeNotifications
	d.State, d.Reason = "partial", "incomplete_evidence"
	d.Counts = &api.ObsServingProbeNotificationCounts{Unknown: 1, Disabled: 1}
	d.Channels[0] = api.ObsServingProbeNotification{Node: "fsn-2", Channel: "webhook", State: "unknown", Reason: "recording_unknown", EvidenceReason: reason, EvaluatedAt: d.Channels[0].EvaluatedAt}
	return out
}

func TestObsNotificationEvidenceReasonsHumanAndJSON(t *testing.T) {
	for _, reason := range operatorobs.ServingProbeNotificationEvidenceReasons() {
		t.Run(reason, func(t *testing.T) {
			stdout, stderr := obsBundleTestStreams(t)
			fleet := servingUnknownNotificationCLIResponse(reason)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(fleet) }))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_APID_URL", srv.URL)
			t.Setenv("FAAS_ADMIN_TOKEN", "admin")
			for _, args := range [][]string{nil, {"--json"}} {
				stdout.Reset()
				stderr.Reset()
				if code := cmdObsServingHealth(args); code != 0 || !strings.Contains(stdout.String(), reason) {
					t.Fatalf("fleet CLI lost unknown cause: %d %s %s", code, stdout, stderr)
				}
			}
			node := nodeNotificationCLIResponse()
			node.State, node.Reason = "partial", "incomplete_evidence"
			node.Channels[0] = fleet.ProbeNotifications.Channels[0]
			node.Channels[0].Node = node.Node
			obsNodeTestAPIDWithNotifications(t, nil, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(node) })
			for _, args := range [][]string{{"fsn-3"}, {"fsn-3", "--json"}} {
				stdout.Reset()
				stderr.Reset()
				if code := cmdObsNode(args); code != 0 || !strings.Contains(stdout.String(), reason) {
					t.Fatalf("node CLI lost unknown cause: %d %s %s", code, stdout, stderr)
				}
			}
			checks, retained, _ := assessObsDeployment(obsDeploymentSnapshot{serving: obsDeploymentServingRead{value: fleet}}, fleet.GeneratedAt.Add(-time.Hour), fleet.GeneratedAt)
			if retained == nil || retained.ProbeNotifications.Channels[0].EvidenceReason != reason {
				t.Fatal("deployment report lost safe unknown evidence")
			}
			if checks[0].State != "passed" {
				t.Fatal("unknown notification evidence became a serving qualification gate")
			}
		})
	}
}

func TestObsNotificationEvidenceReasonsRequireCurrentEvaluation(t *testing.T) {
	for _, reason := range []string{"no_submission_yet", "telemetry_missing", "telemetry_invalid", "source_unavailable"} {
		out := servingUnknownNotificationCLIResponse(reason)
		if !validObsServingHealth(out) {
			t.Fatal("current unknown evidence was rejected")
		}
		stale := out.GeneratedAt.Add(-121 * time.Second)
		out.ProbeNotifications.Channels[0].EvaluatedAt = &stale
		if validObsServingHealth(out) {
			t.Fatal("stale evidence cause was printed as current")
		}
		out.ProbeNotifications.Channels[0].EvaluatedAt = nil
		if operatorobs.ServingProbeNotificationsValid(out) {
			t.Fatal("detailed evidence cause lacked its evaluation clock")
		}
	}
	known := servingNotificationCLIResponse()
	known.ProbeNotifications.Channels[0].EvidenceReason = "telemetry_invalid"
	if validObsServingHealth(known) {
		t.Fatal("an unknown-evidence explanation was accepted on a known outcome")
	}
}

func TestObsNotificationEvidenceRejectsUnsafeDetail(t *testing.T) {
	stdout, stderr := obsBundleTestStreams(t)
	fleet := servingUnknownNotificationCLIResponse("https://private-provider/token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(fleet) }))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "admin")
	for _, args := range [][]string{nil, {"--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsServingHealth(args); code != 1 || strings.Contains(stdout.String()+stderr.String(), "private-provider") {
			t.Fatal("unreviewed evidence detail reached fleet output")
		}
	}
	checks, retained, _ := assessObsDeployment(obsDeploymentSnapshot{serving: obsDeploymentServingRead{value: fleet}}, fleet.GeneratedAt.Add(-time.Hour), fleet.GeneratedAt)
	if checks[0].State != "passed" || retained == nil || retained.ProbeNotifications.Reason != "metadata_inconsistent" || len(retained.ProbeNotifications.Channels) != 0 {
		t.Fatal("unsafe optional detail leaked into deployment evidence or blocked serving")
	}
	node := nodeNotificationCLIResponse()
	node.State, node.Reason = "partial", "incomplete_evidence"
	node.Channels[0] = fleet.ProbeNotifications.Channels[0]
	node.Channels[0].Node = node.Node
	obsNodeTestAPIDWithNotifications(t, nil, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(node) })
	for _, args := range [][]string{{"fsn-3"}, {"fsn-3", "--json"}} {
		stdout.Reset()
		stderr.Reset()
		if code := cmdObsNode(args); code != 0 || !strings.Contains(stdout.String(), "invalid_probe_notifications") || strings.Contains(stdout.String()+stderr.String(), "private-provider") {
			t.Fatal("unreviewed evidence detail reached node output or blocked node inspection")
		}
	}
}
