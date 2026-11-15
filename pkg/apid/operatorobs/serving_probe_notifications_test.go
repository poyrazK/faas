package operatorobs

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func notificationFixtureRow(metric, node, channel string, value float64) promql.VectorSample {
	row := servingFixtureRow(metric, node, "", value, true)
	if channel != "" {
		row.Labels["channel"] = channel
	}
	return row
}

func (f *servingFixture) notification(index int, metric, node, channel string, value float64, now time.Time) {
	f.rows[index] = append(f.rows[index], notificationFixtureRow(metric, node, channel, value))
	f.rows[index+1] = append(f.rows[index+1], notificationFixtureRow(metric, node, channel, float64(now.Add(-10*time.Second).Unix())))
}

func notificationServingFixture(now time.Time) servingFixture {
	f := healthyServingFixture(now)
	f.notification(6, probeRun, "fsn-2", "", float64(now.Add(-time.Minute).Unix()), now)
	f.notification(6, probeRun, "fsn-3", "", float64(now.Add(-time.Minute).Unix()), now)
	f.notification(6, notificationRecording+"state", "fsn-2", "webhook", 3, now)
	f.notification(6, notificationRecording+"state", "fsn-2", "heartbeat", 0, now)
	f.notification(6, notificationRecording+"state", "fsn-3", "webhook", 1, now)
	f.notification(6, notificationRecording+"state", "fsn-3", "heartbeat", 1, now)
	f.notification(8, notificationRecording+"last_attempt_success", "fsn-2", "webhook", 0, now)
	f.notification(8, notificationRecording+"last_attempt_failure_code", "fsn-2", "webhook", 4, now)
	f.notification(10, notificationRecording+"last_attempt_timestamp_seconds", "fsn-2", "webhook", float64(now.Add(-24*time.Hour).Unix()), now)
	f.notification(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2", "webhook", float64(now.Add(-48*time.Hour).Unix()), now)
	return f
}

func TestServingProbeNotificationEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	set := func(value float64) func(*promql.VectorSample) {
		return func(row *promql.VectorSample) { row.Value = value }
	}
	for _, tc := range []struct {
		name, state, reason, code string
		mutate                    func(*servingFixture)
	}{
		{"historical failed submission", "last_submission_failed", "", "timeout", func(*servingFixture) {}},
		{"successful submission", "last_submission_succeeded", "", "none", func(f *servingFixture) {
			f.change(6, notificationRecording+"state", "fsn-2", func(row *promql.VectorSample) {
				if row.Labels["channel"] == "webhook" {
					row.Value = 2
				}
			})
			f.change(8, notificationRecording+"last_attempt_success", "fsn-2", set(1))
			f.change(8, notificationRecording+"last_attempt_failure_code", "fsn-2", set(0))
			f.change(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2", set(float64(now.Add(-24*time.Hour).Unix())))
		}},
		{"unknown failure category", "last_submission_failed", "", "unknown", func(f *servingFixture) {
			f.change(8, notificationRecording+"last_attempt_failure_code", "fsn-2", set(8))
		}},
		{"no success ever observed", "last_submission_failed", "", "timeout", func(f *servingFixture) {
			f.remove(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2")
			f.remove(11, notificationRecording+"last_success_timestamp_seconds", "fsn-2")
		}},
		{"unknown code", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(8, notificationRecording+"last_attempt_failure_code", "fsn-2", set(9))
		}},
		{"fractional code", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(8, notificationRecording+"last_attempt_failure_code", "fsn-2", set(4.5))
		}},
		{"probe-only code", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(8, notificationRecording+"last_attempt_failure_code", "fsn-2", set(7))
		}},
		{"contradictory success bit", "unknown", "metadata_inconsistent", "", func(f *servingFixture) { f.change(8, notificationRecording+"last_attempt_success", "fsn-2", set(1)) }},
		{"missing metadata", "unknown", "metadata_inconsistent", "", func(f *servingFixture) { f.remove(8, notificationRecording+"last_attempt_failure_code", "fsn-2") }},
		{"stale state recording", "unknown", "recording_not_current", "", func(f *servingFixture) {
			f.change(7, notificationRecording+"state", "fsn-2", set(float64(now.Add(-121*time.Second).Unix())))
		}},
		{"future state recording", "unknown", "recording_not_current", "", func(f *servingFixture) {
			f.change(7, notificationRecording+"state", "fsn-2", set(float64(now.Add(31*time.Second).Unix())))
		}},
		{"mixed metadata evaluation", "unknown", "recording_not_current", "", func(f *servingFixture) {
			f.change(9, notificationRecording+"last_attempt_success", "fsn-2", set(float64(now.Add(-11*time.Second).Unix())))
		}},
		{"frozen run", "unknown", "source_unavailable", "", func(f *servingFixture) {
			f.change(6, probeRun, "fsn-2", set(float64(now.Add(-901*time.Second).Unix())))
		}},
		{"future run", "unknown", "source_unavailable", "", func(f *servingFixture) { f.change(6, probeRun, "fsn-2", set(float64(now.Add(time.Minute).Unix()))) }},
		{"foreign run evaluation", "unknown", "source_unavailable", "", func(f *servingFixture) { f.change(7, probeRun, "fsn-2", set(float64(now.Add(-11*time.Second).Unix()))) }},
		{"future completion", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(10, notificationRecording+"last_attempt_timestamp_seconds", "fsn-2", set(float64(now.Add(time.Minute).Unix())))
		}},
		{"subnanosecond completion cannot become epoch zero", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(10, notificationRecording+"last_attempt_timestamp_seconds", "fsn-2", set(1e-12))
		}},
		{"newer success contradicts failure", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2", set(float64(now.Unix())))
		}},
		{"submillisecond newer success", "unknown", "metadata_inconsistent", "", func(f *servingFixture) {
			f.change(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2", set(float64(now.Add(-24*time.Hour).Unix())+0.0001))
		}},
		{"missing optional value with an orphan clock", "unknown", "metadata_inconsistent", "", func(f *servingFixture) { f.remove(10, notificationRecording+"last_success_timestamp_seconds", "fsn-2") }},
		{"query failure", "unknown", "query_failed", "", func(f *servingFixture) { f.fail[8] = true }},
		{"partial query warning", "unknown", "query_failed", "", func(f *servingFixture) { f.warning[11] = true }},
		{"duplicate series", "unknown", "query_failed", "", func(f *servingFixture) { f.rows[8] = append(f.rows[8], f.rows[8][0]) }},
		{"nonfinite value", "unknown", "query_failed", "", func(f *servingFixture) { f.rows[8][0].Value = math.NaN() }},
		{"private label", "unknown", "query_failed", "", func(f *servingFixture) { f.rows[8][0].Labels["url"] = "https://private/token" }},
		{"invalid channel", "unknown", "query_failed", "", func(f *servingFixture) { f.rows[8][0].Labels["channel"] = "private-provider" }},
		{"older coverage query can fail independently", "last_submission_failed", "", "timeout", func(f *servingFixture) { f.fail[2] = true }},
		{"evidence-status query can fail independently", "last_submission_failed", "", "timeout", func(f *servingFixture) { f.fail[12] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := notificationServingFixture(now)
			tc.mutate(&f)
			srv := f.server(t, now)
			out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if tc.name != "older coverage query can fail independently" && out.State != "healthy" {
				t.Fatal("submission evidence changed serving state")
			}
			if !ServingProbeNotificationsValid(out) || out.ProbeNotifications == nil || *out.ProbeNotifications.ExpectedChannels != 4 {
				t.Fatalf("invalid notification section: %+v", out.ProbeNotifications)
			}
			var item api.ObsServingProbeNotification
			for _, row := range out.ProbeNotifications.Channels {
				if row.Node == "fsn-2" && row.Channel == "webhook" {
					item = row
				}
			}
			if item.State != tc.state || item.Reason != tc.reason || item.FailureCode != tc.code {
				t.Fatalf("submission outcome: %+v", item)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "https://") {
				t.Fatalf("raw source metadata leaked: %s", body)
			}
		})
	}
}

func TestServingProbeNotificationPreviewAndBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := servingFixture{}
	f.rows[0] = []promql.VectorSample{servingFixtureRow(probeConfigured, "", "", 1, false), servingFixtureRow(probePairs, "", "", api.ObsServingProbePairsMax, false), servingFixtureRow("up", "", "", 1, false)}
	for _, row := range f.rows[0] {
		f.rows[1] = append(f.rows[1], servingFixtureRow(row.Labels[probeMetricLabel], "", "", float64(now.Add(-10*time.Second).Unix()), false))
	}
	for i := range api.ObsServingProbePairsMax {
		node := fmt.Sprintf("node-%04d", i)
		for _, spec := range []struct {
			metric string
			value  float64
			index  int
			url    string
		}{
			{probeExpected, 1, 0, servingFixtureURL}, {probeBudget, 2, 0, ""}, {probeAge, 900, 0, ""},
			{probeState, 0, 2, servingFixtureURL}, {probeEvalBudget, 2, 2, ""}, {probeEvalAge, 900, 2, ""}, {probeRun, float64(now.Add(-time.Minute).Unix()), 2, ""},
		} {
			f.rows[spec.index] = append(f.rows[spec.index], servingFixtureRow(spec.metric, node, spec.url, spec.value, spec.index == 2))
			f.rows[spec.index+1] = append(f.rows[spec.index+1], servingFixtureRow(spec.metric, node, spec.url, float64(now.Add(-10*time.Second).Unix()), spec.index == 2))
		}
		f.notification(6, probeRun, node, "", float64(now.Add(-time.Minute).Unix()), now)
		for _, channel := range ServingProbeNotificationChannels() {
			f.notification(6, notificationRecording+"state", node, channel, 3, now)
			f.notification(8, notificationRecording+"last_attempt_success", node, channel, 0, now)
			f.notification(8, notificationRecording+"last_attempt_failure_code", node, channel, 4, now)
			f.notification(10, notificationRecording+"last_attempt_timestamp_seconds", node, channel, float64(now.Add(-time.Minute).Unix()), now)
			f.notification(10, notificationRecording+"last_success_timestamp_seconds", node, channel, float64(now.Add(-time.Hour).Unix()), now)
			f.notification(12, notificationRecording+"evidence_status", node, channel, 0, now)
		}
	}
	for i, rows := range f.rows {
		if len(rows) > 4096 {
			t.Fatalf("query %d exceeded strict vector bound: %d", i, len(rows))
		}
	}
	srv := f.server(t, now)
	out := ServingHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
	d := out.ProbeNotifications
	if !ServingProbeNotificationsValid(out) || out.State != "healthy" || d.State != "available" || *d.ExpectedChannels != 2000 || d.Counts.LastSubmissionFailed != 2000 || len(d.Channels) != api.ObsServingProbeNotificationItemsMax || !d.Truncated || d.Omitted != 2000-len(d.Channels) {
		t.Fatalf("maximum fleet lost counts or changed health: %+v", d)
	}
}

func TestServingProbeNotificationCountsPreferFailures(t *testing.T) {
	items := make([]api.ObsServingProbeNotification, 0, 100)
	for i := range 100 {
		state := "disabled"
		if i >= 90 {
			state = "last_submission_failed"
		} else if i >= 60 {
			state = "unknown"
		}
		items = append(items, api.ObsServingProbeNotification{Node: fmt.Sprintf("node-%03d", i), Channel: "webhook", State: state})
	}
	out := summarizeServingProbeNotifications(items)
	if out.Counts.LastSubmissionFailed != 10 || out.Counts.Unknown != 30 || out.Channels[0].State != "last_submission_failed" || out.Channels[10].State != "unknown" || out.Omitted != 68 {
		t.Fatal("preview lost full counts or prioritized disabled channels")
	}
}
