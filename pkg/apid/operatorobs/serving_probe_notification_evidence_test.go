package operatorobs

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestServingProbeNotificationUnknownEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	set := func(value float64) func(*promql.VectorSample) {
		return func(row *promql.VectorSample) { row.Value = value }
	}
	for _, tc := range []struct {
		name, reason string
		status       float64
		evaluated    bool
		mutate       func(*servingFixture)
	}{
		{"quiet channel", "no_submission_yet", 1, true, nil},
		{"missing fields", "telemetry_missing", 2, true, nil},
		{"invalid fields", "telemetry_invalid", 3, true, nil},
		{"unavailable source", "source_unavailable", 4, true, func(f *servingFixture) { f.change(6, probeRun, "fsn-3", set(float64(now.Add(-time.Hour).Unix()))) }},
		{"older rules", "recording_unknown", 1, true, func(f *servingFixture) { f.rows[12], f.rows[13] = nil, nil }},
		{"contradictory status", "metadata_inconsistent", 0, false, nil},
		{"unreviewed status", "metadata_inconsistent", 5, false, nil},
		{"fractional status", "metadata_inconsistent", 1.5, false, nil},
		{"failed query", "query_failed", 1, false, func(f *servingFixture) { f.fail[12] = true }},
		{"partial query", "query_failed", 1, false, func(f *servingFixture) { f.warning[13] = true }},
		{"duplicate status", "query_failed", 1, false, func(f *servingFixture) { f.rows[12] = append(f.rows[12], f.rows[12][0]) }},
		{"private label", "query_failed", 1, false, func(f *servingFixture) { f.rows[12][0].Labels["destination"] = "https://private/token" }},
		{"stale status", "recording_not_current", 1, false, func(f *servingFixture) {
			f.change(13, notificationRecording+"evidence_status", "fsn-3", set(float64(now.Add(-121*time.Second).Unix())))
		}},
		{"mixed evaluations", "recording_not_current", 1, false, func(f *servingFixture) {
			f.change(13, notificationRecording+"evidence_status", "fsn-3", set(float64(now.Add(-11*time.Second).Unix())))
		}},
		{"future status", "recording_not_current", 1, false, func(f *servingFixture) {
			f.change(13, notificationRecording+"evidence_status", "fsn-3", set(float64(now.Add(time.Minute).Unix())))
		}},
		{"missing status clock", "recording_not_current", 1, false, func(f *servingFixture) { f.rows[13] = nil }},
		{"orphan status clock", "metadata_inconsistent", 1, false, func(f *servingFixture) { f.rows[12] = nil }},
		{"frozen probe run", "source_unavailable", 1, false, func(f *servingFixture) { f.change(6, probeRun, "fsn-3", set(float64(now.Add(-time.Hour).Unix()))) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := notificationServingFixture(now)
			f.notification(12, notificationRecording+"evidence_status", "fsn-3", "webhook", tc.status, now)
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			srv := f.server(t, now)
			fleet := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if fleet.State != "healthy" || !ServingProbeNotificationsValid(fleet) {
				t.Fatal("unknown submission evidence changed serving health or became invalid")
			}
			fixture := &nodeNotificationFixture{source: f}
			scoped := fixture.server(t, "fsn-3", now)
			selection, _ := NodeProbeNotificationsSelection("fsn-3", "", false)
			node := NodeProbeNotifications(t.Context(), promql.NewClient(scoped.URL, scoped.Client()), selection, now)
			if !NodeProbeNotificationsValid(node) || node.Expected == nil || !*node.Expected || len(node.Channels) != 2 {
				t.Fatal("unknown submission evidence erased independent node intent")
			}
			for _, items := range [][]api.ObsServingProbeNotification{fleet.ProbeNotifications.Channels, node.Channels} {
				found := false
				for _, item := range items {
					if item.Node != "fsn-3" || item.Channel != "webhook" {
						continue
					}
					found = true
					wantReason, wantDetail := tc.reason, ""
					if tc.evaluated && slices.Contains(ServingProbeNotificationEvidenceReasons(), tc.reason) {
						wantReason, wantDetail = "recording_unknown", tc.reason
					}
					if item.State != "unknown" || item.Reason != wantReason || item.EvidenceReason != wantDetail || (item.EvaluatedAt != nil) != tc.evaluated || item.Enabled != nil || item.LastRunAt != nil || item.LastAttemptAt != nil || item.LastSuccessAt != nil || item.FailureCode != "" {
						t.Fatalf("unknown evidence fabricated outcome or lost cause: %+v", item)
					}
					legacy := item
					legacy.EvidenceReason = ""
					if !servingProbeNotificationValid(legacy, now, 900) {
						t.Fatal("a client ignoring optional detail lost the coarse outcome contract")
					}
				}
				body, _ := json.Marshal(items)
				if !found || strings.Contains(string(body), "private") || strings.Contains(string(body), "https://") {
					t.Fatal("unknown evidence disappeared or leaked source metadata")
				}
			}
		})
	}
}
