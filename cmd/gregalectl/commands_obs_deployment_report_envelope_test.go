package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func deploymentEnvelopeFaultFixture(t *testing.T) (obsDeploymentReport, *obsDeploymentTimelineRecorder) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentFaultEvidenceFixture(now, "tls", 1)
	s.notifications = deploymentNotificationFaultFixture(now).notifications
	*s.notifications.Signals["alertmanager_failures"].Value = 2
	after := now.Add(-90 * time.Second)
	checks, serving, notifications := assessObsDeployment(s, after, now)
	recorder := newObsDeploymentTimeline(checks)
	recorder.notifications = newObsDeploymentNotificationRecorder(after)
	advisory := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Minute))
	recorder.notifications.observe(&advisory, now, 1)
	probes := obsDeploymentProbeFailures(checks, serving, after, now)
	faults := obsDeploymentNotificationFailures(checks, notifications, after, now)
	history := recorder.observeWithFaults(checks, now, 1, probes, faults)
	report := obsDeploymentReport{SchemaVersion: 1, State: "degraded", Reason: "confirmed_monitoring_fault", HadConfirmedFaults: true,
		Checks: checks, Serving: serving, Notifications: notifications, Timeline: history, Sources: maximumDeploymentSources(now), QualificationEvidence: maximumQualificationEvidence(now), Limitations: []string{obsDeploymentNotificationPreviewLimitation}}
	return report, recorder
}

func TestObsDeploymentReportEnvelopePrioritiesAndImmutableViews(t *testing.T) {
	report, recorder := deploymentEnvelopeFaultFixture(t)
	original, _ := json.MarshalIndent(report, "", "  ")
	noAdvisory := report
	timeline := *report.Timeline
	timeline.ProbeNotifications = nil
	noAdvisory.Timeline = &timeline
	noAdvisory.Limitations = []string{obsDeploymentNotificationEnvelopeLimitation}
	base, _ := json.MarshalIndent(noAdvisory, "", "  ")
	for _, tc := range []struct {
		name    string
		limit   int
		primary bool
	}{
		{"advisory yields first", len(base) + 100, false},
		{"primary details yield after advisory", len(base) - 250, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fitted := fitObsDeploymentReportWithin(report, tc.limit)
			body, err := json.MarshalIndent(fitted, "", "  ")
			if err != nil || len(body)+1 > tc.limit {
				t.Fatalf("fitted report exceeds limit: %d limit=%d err=%v", len(body)+1, tc.limit, err)
			}
			if fitted.State != report.State || fitted.Reason != report.Reason || fitted.HadConfirmedFaults != report.HadConfirmedFaults || fitted.Serving != report.Serving || fitted.Notifications != report.Notifications || fitted.Sources != report.Sources || fitted.QualificationEvidence != report.QualificationEvidence || !reflect.DeepEqual(fitted.Checks, report.Checks) || !reflect.DeepEqual(fitted.Timeline.Checks, report.Timeline.Checks) || len(fitted.Timeline.Transitions) != len(report.Timeline.Transitions) {
				t.Fatal("fitting changed health, current sources or timeline summaries")
			}
			for i, transition := range fitted.Timeline.Transitions {
				before := report.Timeline.Transitions[i]
				if transition.Check != before.Check || transition.State != before.State || transition.Reason != before.Reason || transition.Attempt != before.Attempt || !transition.At.Equal(before.At) {
					t.Fatal("fitting changed a health transition")
				}
			}
			if tc.primary {
				if fitted.Timeline.ProbeNotifications != nil || !slices.Contains(fitted.Limitations, obsDeploymentNotificationEnvelopeLimitation) || fitted.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots != 1 || fitted.Timeline.ProbeEvidence.EnvelopeOmittedItems != 1 || !fitted.Timeline.ProbeEvidence.Truncated || fitted.Timeline.ProbeEvidence.RetainedItems != 0 || fitted.Timeline.Transitions[0].ProbeFailures != nil || fitted.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots != 0 {
					t.Fatal("oldest primary detail was not omitted after advisory history")
				}
				var human strings.Builder
				writeObsDeploymentHuman(&human, fitted)
				if !strings.Contains(human.String(), "envelope_omitted_items=1 envelope_omitted_snapshots=1") {
					t.Fatal("human output hid primary envelope omissions")
				}
			} else if fitted.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots != 0 || fitted.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots != 0 {
				t.Fatal("primary detail was omitted before advisory history was exhausted")
			}
			after, _ := json.MarshalIndent(report, "", "  ")
			if string(original) != string(after) || !reflect.DeepEqual(report.Timeline, recorder.snapshot()) {
				t.Fatal("fitting mutated the input report or retained recorder")
			}
		})
	}
}

func TestObsDeploymentReportEnvelopeAccountsBothFaultScopes(t *testing.T) {
	report, _ := deploymentEnvelopeFaultFixture(t)
	minimal := report
	timeline := *report.Timeline
	timeline.Transitions = slices.Clone(timeline.Transitions)
	timeline.ProbeNotifications = nil
	probeStats, notificationStats := *timeline.ProbeEvidence, *timeline.NotificationEvidence
	timeline.ProbeEvidence, timeline.NotificationEvidence = &probeStats, &notificationStats
	for omitOldestObsDeploymentFaultSnapshot(&timeline) {
	}
	minimal.Timeline = &timeline
	minimal.Limitations = []string{obsDeploymentNotificationEnvelopeLimitation}
	body, _ := json.MarshalIndent(minimal, "", "  ")
	fitted := fitObsDeploymentReportWithin(report, len(body)+1)
	if fitted.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots != 1 || fitted.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots != 2 || fitted.Timeline.NotificationEvidence.EnvelopeOmittedItems != 2 || fitted.Timeline.NotificationEvidence.RetainedItems != 0 || fitted.Timeline.NotificationEvidence.RetainedBytes != 0 {
		t.Fatalf("envelope omission counts disagree across scopes: %+v", fitted.Timeline)
	}
	if fitted.Timeline.ProbeEvidence.DroppedItems != report.Timeline.ProbeEvidence.DroppedItems || fitted.Timeline.NotificationEvidence.DroppedItems != report.Timeline.NotificationEvidence.DroppedItems {
		t.Fatal("report-only omissions were counted as permanent recorder drops")
	}
	// A later, larger envelope can expose the original retained evidence again.
	full := fitObsDeploymentReport(report)
	if full.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots != 0 || full.Timeline.NotificationEvidence.RetainedItems != 2 || full.Timeline.ProbeEvidence.RetainedItems != 1 {
		t.Fatal("fitting permanently evicted primary history")
	}
}
