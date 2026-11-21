package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestObsDeploymentQualificationHistoryEnvelopePriorities(t *testing.T) {
	report, recorder := deploymentEnvelopeFaultFixture(t)
	report.QualificationEvidence.History = maximumQualificationHistory(time.Now().UTC(), report.QualificationEvidence.Checks)
	before, _ := json.MarshalIndent(report, "", "  ")
	minimal := omitObsDeploymentNotificationHistory(report, *report.Timeline)
	evidence := *minimal.QualificationEvidence
	history := cloneObsDeploymentEvidenceHistory(evidence.History)
	history.EnvelopeOmittedEvents = len(history.Events)
	history.Events = history.Events[:0]
	history.account()
	evidence.History = history
	minimal.QualificationEvidence = &evidence
	body, _ := json.MarshalIndent(minimal, "", "  ")
	for _, primary := range []bool{false, true} {
		limit := len(body) + 1
		if primary {
			limit -= 250
		}
		fitted := fitObsDeploymentReportWithin(report, limit)
		encoded, err := json.MarshalIndent(fitted, "", "  ")
		if err != nil || len(encoded)+1 > limit {
			t.Fatalf("fitted report exceeds its envelope: %d limit=%d err=%v", len(encoded)+1, limit, err)
		}
		got := fitted.QualificationEvidence
		if fitted.Timeline.ProbeNotifications != nil || got.History.RetainedEvents != 0 || got.History.EnvelopeOmittedEvents != report.QualificationEvidence.History.RetainedEvents || !got.History.Truncated || got.History.DroppedEvents != report.QualificationEvidence.History.DroppedEvents || got.History.TotalEvents != report.QualificationEvidence.History.TotalEvents || !reflect.DeepEqual(got.Checks, report.QualificationEvidence.Checks) || !reflect.DeepEqual(got.ObservedAt, report.QualificationEvidence.ObservedAt) || fitted.Sources != report.Sources || fitted.Serving != report.Serving || fitted.Notifications != report.Notifications || fitted.State != report.State || fitted.HadConfirmedFaults != report.HadConfirmedFaults {
			t.Fatal("envelope trimming changed current evidence or permanent history counts")
		}
		omitted := fitted.Timeline.ProbeEvidence.EnvelopeOmittedSnapshots + fitted.Timeline.NotificationEvidence.EnvelopeOmittedSnapshots
		if primary && omitted == 0 || !primary && omitted != 0 {
			t.Fatal("primary fault detail did not yield after qualification events")
		}
		var human strings.Builder
		writeObsDeploymentEvidence(&human, got)
		if !strings.Contains(human.String(), "envelope_omitted_events=") || !strings.Contains(human.String(), "first_passed_at=") {
			t.Fatal("human output hid history omission or retained timing")
		}
	}
	after, _ := json.MarshalIndent(report, "", "  ")
	if string(after) != string(before) || !reflect.DeepEqual(report.Timeline, recorder.snapshot()) {
		t.Fatal("envelope trimming rewrote a saved view or recorder")
	}
	// Fitting a later larger view can expose the recorder's history again.
	full := fitObsDeploymentReport(report)
	if full.QualificationEvidence.History.EnvelopeOmittedEvents != 0 {
		t.Fatal("report-only omission permanently evicted qualification history")
	}
}

func TestObsDeploymentQualificationHistoryEnvelopeWithoutTimeline(t *testing.T) {
	evidence := maximumQualificationEvidence(time.Now().UTC())
	evidence.History = maximumQualificationHistory(time.Now().UTC(), evidence.Checks)
	report := obsDeploymentReport{QualificationEvidence: evidence}
	minimal := report
	copy := *evidence
	history := cloneObsDeploymentEvidenceHistory(evidence.History)
	history.EnvelopeOmittedEvents = len(history.Events)
	history.Events = history.Events[:0]
	history.account()
	copy.History, minimal.QualificationEvidence = history, &copy
	body, _ := json.MarshalIndent(minimal, "", "  ")
	fitted := fitObsDeploymentReportWithin(report, len(body)+1)
	encoded, err := json.MarshalIndent(fitted, "", "  ")
	if err != nil || len(encoded)+1 > len(body)+1 || fitted.QualificationEvidence.History.EnvelopeOmittedEvents == 0 || !reflect.DeepEqual(fitted.QualificationEvidence.Checks, evidence.Checks) {
		t.Fatal("qualification history could not yield without a health timeline")
	}
}
