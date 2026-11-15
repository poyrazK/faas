package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func observeQualificationProgress(r *obsDeploymentEvidenceRecorder, s obsDeploymentSnapshot, after, at time.Time) *obsDeploymentEvidence {
	checks, serving, notifications := assessObsDeployment(s, after, at)
	return r.observe(checks, s, serving, notifications, after, at)
}

func TestObsDeploymentQualificationProgressCausesAndWaiting(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	after := start.Add(-90 * time.Second)
	checks, _, _ := assessObsDeployment(deploymentSnapshotTest(start), after, start)
	r := newObsDeploymentEvidence(checks)
	first := observeQualificationProgress(r, blockedQualificationSnapshot(start, after), after, start)
	saved, _ := json.Marshal(first)
	if first.History.TotalEvents != 3 || first.History.Truncated {
		t.Fatal("first observations did not get independent events")
	}
	repeated := observeQualificationProgress(r, blockedQualificationSnapshot(start.Add(10*time.Second), after), after, start.Add(10*time.Second))
	if repeated.History.TotalEvents != 3 || repeated.Checks[0].Progress.ObservedBlockedSeconds != 10 {
		t.Fatal("clock updates made new causes or lost elapsed blocked time")
	}
	changed := func(at time.Time) obsDeploymentSnapshot {
		s := blockedQualificationSnapshot(at, after)
		s.notifications.Signals["prometheus_queue_length"] = api.ObsNotificationSignal{State: "no_samples", Reason: "insufficient_samples", MaxAgeSeconds: 120}
		return s
	}
	second := observeQualificationProgress(r, changed(start.Add(20*time.Second)), after, start.Add(20*time.Second))
	last := second.History.Events[len(second.History.Events)-1]
	if second.History.TotalEvents != 4 || last.Check != "notification_pipeline" || last.State != "blocked" || last.CauseSHA256 == first.History.Events[1].CauseSHA256 || last.Blockers.Items[0].Reason != "missing_samples" || !second.Checks[1].Progress.BlockersSince.Equal(start.Add(20*time.Second)) || !second.Checks[1].Progress.StateSince.Equal(start) {
		t.Fatal("cause changes inside an unknown check were hidden")
	}
	unavailable := changed(start.Add(30 * time.Second))
	unavailable.serving.err, unavailable.notificationErr = errors.New("private-read-error"), errors.New("private-provider-error")
	lost := observeQualificationProgress(r, unavailable, after, start.Add(30*time.Second))
	if lost.History.TotalEvents != 7 || lost.Checks[0].Progress.BlockersSince != nil {
		t.Fatal("source loss was shown as an active blocker")
	}
	resumed := observeQualificationProgress(r, changed(start.Add(50*time.Second)), after, start.Add(50*time.Second))
	if resumed.History.TotalEvents != 10 || resumed.Checks[1].Progress.ObservedUnavailableSeconds != 20 || resumed.Checks[1].Progress.ObservedBlockedSeconds != 30 || !resumed.Checks[1].Progress.BlockersSince.Equal(start.Add(50*time.Second)) {
		t.Fatal("unavailable time was folded into readable blocked time")
	}
	passed := observeQualificationProgress(r, deploymentSnapshotTest(start.Add(60*time.Second)), after, start.Add(60*time.Second))
	final := observeQualificationProgress(r, deploymentSnapshotTest(start.Add(70*time.Second)), after, start.Add(70*time.Second))
	if passed.History.TotalEvents != 13 || final.History.TotalEvents != 13 {
		t.Fatal("passing clock updates added events")
	}
	for _, check := range final.Checks {
		p := check.Progress
		if check.State != "passed" || !p.FirstObservedAt.Equal(start) || !p.FirstBlockedAt.Equal(start) || !p.FirstPassedAt.Equal(start.Add(60*time.Second)) || !p.StateSince.Equal(start.Add(60*time.Second)) || p.BlockersSince != nil || p.ObservedBlockedSeconds != 40 || p.ObservedUnavailableSeconds != 20 {
			t.Fatalf("recovery lost qualification timing: %+v", p)
		}
	}
	body, _ := json.Marshal(first)
	if string(body) != string(saved) {
		t.Fatal("later observations mutated an earlier saved report")
	}
	// A saved view also owns its clocks and detail pointers.
	*final.Checks[0].Progress.FirstBlockedAt = time.Time{}
	*final.History.Events[0].Blockers.Items[0].ObservedAt = time.Time{}
	latest := r.snapshot(&start)
	if latest.Checks[0].Progress.FirstBlockedAt.IsZero() || latest.History.Events[0].Blockers.Items[0].ObservedAt.IsZero() {
		t.Fatal("saved report pointers could rewrite the recorder")
	}
	var human strings.Builder
	writeObsDeploymentEvidence(&human, latest)
	if !strings.Contains(human.String(), "observed_blocked_seconds=40 observed_unavailable_seconds=20") || !strings.Contains(human.String(), "qualification_history historical=true") || !strings.Contains(human.String(), "state=blocked") || strings.Contains(human.String(), "private-") {
		t.Fatal("human history omitted timing, lost earlier causes or leaked source errors")
	}
}

func TestObsDeploymentQualificationProgressFingerprintIncludesOmittedPairs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	s := deploymentSnapshotTest(now)
	probe := s.serving.value.Probes[0]
	s.serving.value.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax)
	run := after.Add(-time.Second)
	for i := range s.serving.value.Probes {
		s.serving.value.Probes[i] = probe
		s.serving.value.Probes[i].URLID = fmt.Sprintf("%064x", i)
		if i >= api.ObsServingProbePairsMax-6 {
			s.serving.value.Probes[i].LastRunAt = &run
		}
	}
	expected := len(s.serving.value.Probes)
	s.serving.value.Inventory.ExpectedPairs = &expected
	qualificationTestCounts(&s)
	checks, _, _ := assessObsDeployment(s, after, now)
	r := newObsDeploymentEvidence(checks)
	first := observeQualificationProgress(r, s, after, now)
	// The changed pair is outside the four visible blockers and stays sorted.
	s.serving.value.Probes[len(s.serving.value.Probes)-1].URLID = fmt.Sprintf("%064x", api.ObsServingProbePairsMax)
	second := observeQualificationProgress(r, s, after, now)
	if first.History.TotalEvents != 3 || second.History.TotalEvents != 4 || !reflect.DeepEqual(first.Checks[0].Blockers, second.Checks[0].Blockers) || second.History.Events[3].CauseSHA256 == first.History.Events[0].CauseSHA256 || len(second.History.Events[3].CauseSHA256) != 64 {
		t.Fatal("a cause change beyond the retained preview was hidden")
	}
	// Policy changes are semantic; observation clocks alone are not.
	s.serving.value.Probes[len(s.serving.value.Probes)-1].MaxResultAgeSeconds++
	third := observeQualificationProgress(r, s, after, now)
	if third.History.TotalEvents != 5 {
		t.Fatal("a changed result-age policy did not change the cause")
	}
}

func TestObsDeploymentQualificationProgressFaultsAndClockRegression(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	after := start.Add(-90 * time.Second)
	checks, _, _ := assessObsDeployment(deploymentSnapshotTest(start), after, start)
	r := newObsDeploymentEvidence(checks)
	observeQualificationProgress(r, blockedQualificationSnapshot(start, after), after, start)
	fault := deploymentSnapshotTest(start.Add(5 * time.Second))
	*fault.notifications.Signals["alertmanager_failures"].Value = 1
	confirmed := observeQualificationProgress(r, fault, after, start.Add(5*time.Second))
	if confirmed.Checks[1].State != "faulted" || confirmed.Checks[1].Progress.FirstPassedAt != nil || confirmed.Checks[1].Progress.ObservedBlockedSeconds != 5 || confirmed.Checks[1].Progress.BlockersSince != nil || confirmed.Checks[1].LastBlocked == nil {
		t.Fatal("a confirmed fault became a qualification wait or a pass")
	}
	observeQualificationProgress(r, blockedQualificationSnapshot(start.Add(4*time.Second), after), after, start.Add(4*time.Second))
	final := observeQualificationProgress(r, deploymentSnapshotTest(start.Add(8*time.Second)), after, start.Add(8*time.Second))
	if final.Checks[1].Progress.ObservedBlockedSeconds != 8 || !final.Checks[1].Progress.FirstPassedAt.Equal(start.Add(8*time.Second)) {
		t.Fatal("coordinator clock regression counted an interval twice")
	}
}

func TestObsDeploymentQualificationProgressHistoryBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"event count", "serialized bytes"} {
		t.Run(mode, func(t *testing.T) {
			history := newObsDeploymentEvidenceHistory()
			for i := range api.ObsPostDeployEvidenceEventsMax + 5 {
				event := obsDeploymentEvidenceEvent{At: now.Add(time.Duration(i) * time.Second), Check: "serving_probes", State: "passed"}
				if mode == "serialized bytes" {
					event.State = "blocked"
					event.Blockers = maximumQualificationEvidence(now).Checks[0].LastBlocked.Blockers
				}
				history.append(event)
			}
			if history.TotalEvents != api.ObsPostDeployEvidenceEventsMax+5 || history.TotalEvents != history.RetainedEvents+history.DroppedEvents || history.EnvelopeOmittedEvents != 0 || !history.Truncated || history.RetainedBytes > history.ByteLimit || history.RetainedEvents == 0 || !history.Events[len(history.Events)-1].At.Equal(now.Add(20*time.Second)) {
				t.Fatalf("history bounds or omission counts disagree: %+v", history)
			}
			if mode == "event count" && history.RetainedEvents != history.EventLimit || mode == "serialized bytes" && history.RetainedEvents >= history.EventLimit {
				t.Fatal("the independent event or byte cap was not exercised")
			}
			body, err := json.MarshalIndent(history.Events, "      ", "  ")
			if err != nil || len(body)+len("      ")+1 != history.RetainedBytes {
				t.Fatal("history bytes did not include pretty indentation")
			}
		})
	}
}

func TestObsDeploymentQualificationProgressSummarySurvivesRollover(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	after := start.Add(-90 * time.Second)
	checks, _, _ := assessObsDeployment(deploymentSnapshotTest(start), after, start)
	r := newObsDeploymentEvidence(checks)
	for i := range 21 {
		at := start.Add(time.Duration(i) * time.Second)
		s := blockedQualificationSnapshot(at, after)
		if i%2 == 1 {
			s.notifications.Signals["prometheus_queue_length"] = api.ObsNotificationSignal{State: "no_samples", Reason: "insufficient_samples", MaxAgeSeconds: 120}
		}
		observeQualificationProgress(r, s, after, at)
	}
	recovered := observeQualificationProgress(r, deploymentSnapshotTest(start.Add(50*time.Second)), after, start.Add(50*time.Second))
	if recovered.History.TotalEvents != 26 || recovered.History.DroppedEvents == 0 || !recovered.History.Truncated || !recovered.History.Events[0].At.After(start) {
		t.Fatal("test did not evict the first blocker events")
	}
	for _, check := range recovered.Checks {
		if !check.Progress.FirstBlockedAt.Equal(start) || !check.Progress.FirstPassedAt.Equal(start.Add(50*time.Second)) || check.Progress.ObservedBlockedSeconds != 50 || check.Progress.ObservedUnavailableSeconds != 0 {
			t.Fatal("history eviction changed retained qualification timing")
		}
	}
}
