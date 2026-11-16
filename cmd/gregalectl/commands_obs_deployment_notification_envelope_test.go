package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func notificationEnvelopeTestReport(t *testing.T) (obsDeploymentReport, *obsDeploymentNotificationRecorder) {
	t.Helper()
	now := time.Now().UTC()
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	for i := range 10 {
		at := now.Add(time.Duration(i) * time.Second)
		serving := deploymentNotificationHistoryFixture(at, "fsn-2", "last_submission_failed", at.Add(-time.Hour))
		recorder.observe(&serving, at, i+1)
	}
	return obsDeploymentReport{SchemaVersion: 1, Scope: "post_deployment_monitoring", State: "unknown", Reason: "awaiting_evidence",
		Timeline:    &obsDeploymentTimeline{ProbeNotifications: recorder.snapshot()},
		Limitations: []string{obsDeploymentNotificationPreviewLimitation}}, recorder
}

func TestObsDeploymentNotificationEnvelopePreservesPrimaryAndSnapshots(t *testing.T) {
	report, recorder := notificationEnvelopeTestReport(t)
	before, _ := json.MarshalIndent(report, "", "  ")
	original := report.Timeline.ProbeNotifications
	limit := len(before) - obsDeploymentNotificationEventBytes(original.Events[0]) - obsDeploymentNotificationEventBytes(original.Events[1])/2
	fitted := fitObsDeploymentNotificationHistoryWithin(report, limit)
	bytes, err := json.MarshalIndent(fitted, "", "  ")
	history := fitted.Timeline.ProbeNotifications
	if err != nil || len(bytes)+1 > limit || history == nil || history.EnvelopeOmittedEvents == 0 || !history.Truncated || history.DroppedEvents != original.DroppedEvents || len(history.Events)+history.EnvelopeOmittedEvents != len(original.Events) {
		t.Fatal("envelope fitting hid omissions or exceeded the serialized limit")
	}
	if fitted.State != report.State || fitted.Reason != report.Reason || fitted.HadConfirmedFaults != report.HadConfirmedFaults || !history.HadSubmissionFailures || history.UnresolvedTrackedFailures != original.UnresolvedTrackedFailures {
		t.Fatal("envelope fitting changed primary state or erased tracked failure summaries")
	}
	after, _ := json.MarshalIndent(report, "", "  ")
	if string(before) != string(after) || len(recorder.snapshot().Events) != len(original.Events) || recorder.snapshot().EnvelopeOmittedEvents != 0 {
		t.Fatal("fitting mutated a previous report or the independent recorder")
	}
	roomy := fitObsDeploymentNotificationHistoryWithin(report, len(before)+1)
	if len(roomy.Timeline.ProbeNotifications.Events) != len(original.Events) || roomy.Timeline.ProbeNotifications.EnvelopeOmittedEvents != 0 {
		t.Fatal("transport omissions permanently discarded internal observations")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, fitted)
	if !strings.Contains(human.String(), "envelope_omitted_events=") || !strings.Contains(human.String(), "truncated=true") {
		t.Fatal("human output hid the advisory report envelope limit")
	}
}

func TestObsDeploymentNotificationEnvelopeCanOmitOptionalMetadata(t *testing.T) {
	report, _ := notificationEnvelopeTestReport(t)
	base := report
	timeline := *report.Timeline
	timeline.ProbeNotifications = nil
	base.Timeline = &timeline
	base.Limitations = []string{obsDeploymentNotificationEnvelopeLimitation}
	bytes, _ := json.MarshalIndent(base, "", "  ")
	fitted := fitObsDeploymentNotificationHistoryWithin(report, len(bytes)+1)
	body, err := json.MarshalIndent(fitted, "", "  ")
	if err != nil || len(body)+1 > len(bytes)+1 || fitted.Timeline.ProbeNotifications != nil || !slices.Contains(fitted.Limitations, obsDeploymentNotificationEnvelopeLimitation) {
		t.Fatal("omitted advisory metadata lacked an explicit envelope limitation")
	}
	if report.Timeline.ProbeNotifications == nil || !slices.Contains(report.Limitations, obsDeploymentNotificationPreviewLimitation) {
		t.Fatal("metadata omission mutated the original report")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, fitted)
	if !strings.Contains(human.String(), "advisory=true unassessed=true reason=report_envelope") {
		t.Fatal("human output treated omitted history as an empty successful history")
	}
}
