package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentNotificationHistoryFixture(now time.Time, node, state string, completed time.Time) api.ObsServingHealth {
	out := servingNotificationCLIResponse()
	run := now.Add(-time.Second)
	out.GeneratedAt, out.Inventory.ObservedAt = now, &now
	out.Probes[0].Node, out.Probes[0].EvaluatedAt, out.Probes[0].LastRunAt = node, &now, &run
	d := out.ProbeNotifications
	d.Channels[0] = api.ObsServingProbeNotification{Node: node, Channel: "webhook", State: state, EvaluatedAt: &now}
	off := false
	d.Channels[1] = api.ObsServingProbeNotification{Node: node, Channel: "heartbeat", State: "disabled", Enabled: &off, EvaluatedAt: &now, LastRunAt: &run}
	d.Counts = &api.ObsServingProbeNotificationCounts{Disabled: 1}
	d.State, d.Reason = "available", ""
	item := &d.Channels[0]
	on := true
	switch state {
	case "last_submission_failed":
		item.Enabled, item.LastRunAt, item.LastAttemptAt, item.FailureCode = &on, &run, &completed, "timeout"
		d.Counts.LastSubmissionFailed = 1
	case "last_submission_succeeded":
		item.Enabled, item.LastRunAt, item.LastAttemptAt, item.LastSuccessAt, item.FailureCode = &on, &run, &completed, &completed, "none"
		d.Counts.LastSubmissionSucceeded = 1
	case "unknown":
		item.Reason, item.EvidenceReason = "recording_unknown", "telemetry_missing"
		d.State, d.Reason, d.Counts.Unknown = "partial", "incomplete_evidence", 1
	case "disabled":
		item.Enabled, item.LastRunAt = &off, &run
		d.Counts.Disabled++
		d.Channels[0], d.Channels[1] = d.Channels[1], d.Channels[0]
	}
	if !validObsServingHealth(out) {
		panic("invalid notification history test fixture")
	}
	return out
}

func TestObsDeploymentNotificationHistorySurvivesUnknownDisablementAndRotation(t *testing.T) {
	started := time.Now().UTC().Truncate(time.Second)
	now, calls := started, 0
	failureAt := started.Add(-24 * time.Hour)
	var saved []obsDeploymentReport
	fetch := func(context.Context) obsDeploymentSnapshot {
		calls++
		snapshot := deploymentSnapshotTest(now)
		state := "last_submission_failed"
		if calls == 3 || calls == 5 {
			state = "unknown"
		} else if calls == 4 {
			state = "disabled"
		} else if calls == 6 {
			state = "last_submission_succeeded"
		}
		completed := failureAt
		if calls == 6 {
			completed = now
		}
		snapshot.serving.value = deploymentNotificationHistoryFixture(now, "fsn-2", state, completed)
		if calls == 5 {
			snapshot.serving.value.ProbeNotifications.Channels[0].EvidenceReason = "no_submission_yet"
		}
		if calls != 6 {
			snapshot.notificationErr = errors.New("private-provider-error")
		}
		return snapshot
	}
	opts := obsDeploymentOptions{release: strings.Repeat("a", 40), after: started.Add(-90 * time.Second), wait: 10 * time.Second, poll: time.Second}
	report, code := runObsDeploymentVerification(t.Context(), opts, fetch, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(report obsDeploymentReport) error { saved = append(saved, report); return nil })
	if code != 0 || report.State != "passed" || report.Attempts != 6 || report.HadConfirmedFaults || len(report.Checks) != 3 {
		t.Fatal("advisory history changed deployment qualification")
	}
	history := report.Timeline.ProbeNotifications
	if !history.HadSubmissionFailures || history.UnresolvedTrackedFailures != 0 || history.TrackedChannels != 2 || history.IncompleteObservations != 2 || len(history.Events) != 6 || history.Truncated {
		t.Fatalf("history lost observations or counted duplicate polls: %+v", history)
	}
	var webhook []obsDeploymentNotificationEvent
	for _, event := range history.Events {
		if event.Channel == "webhook" {
			webhook = append(webhook, event)
		}
	}
	want := []string{"last_submission_failed", "unknown", "disabled", "unknown", "last_submission_succeeded"}
	if len(webhook) != len(want) {
		t.Fatalf("webhook observations: %+v", webhook)
	}
	for i, event := range webhook {
		if event.State != want[i] || !event.LatestObservedFailureAt.Equal(failureAt) || event.UnresolvedObservedFailure != (i < len(want)-1) || event.LaterSuccessAfterFailure != (i == len(want)-1) {
			t.Fatalf("unknown, disablement or reset manufactured recovery: %+v", event)
		}
	}
	if *webhook[0].AttemptAtOrAfterBoundary || !*webhook[len(webhook)-1].AttemptAtOrAfterBoundary || !webhook[0].LastAttemptAt.Equal(failureAt) {
		t.Fatal("old submission was attributed to the deployment or its clock was rewritten")
	}
	if len(saved[0].Timeline.ProbeNotifications.Events) != 0 || saved[0].Timeline.ProbeNotifications.LastObservedAt != nil || len(saved[1].Timeline.ProbeNotifications.Events) != 2 || !saved[1].Timeline.ProbeNotifications.Events[0].UnresolvedObservedFailure {
		t.Fatal("later polls mutated an earlier report")
	}
	body, _ := json.Marshal(report)
	if strings.Contains(string(body), "private-provider-error") {
		t.Fatal("notification history leaked upstream text")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, report)
	for _, value := range []string{"probe_notification_history advisory=true", "had_submission_failures=true", "evidence_reason=telemetry_missing", "evidence_reason=no_submission_yet", "attempt_at_or_after_boundary=false", "later_success_after_failure=true"} {
		if !strings.Contains(human.String(), value) {
			t.Fatalf("human report lost %s", value)
		}
	}
}

func TestObsDeploymentNotificationHistoryNeverAddsGateOrPoll(t *testing.T) {
	now := time.Now().UTC()
	snapshot := deploymentSnapshotTest(now)
	snapshot.serving.value = deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
	options := obsDeploymentOptions{release: strings.Repeat("a", 40), after: now.Add(-90 * time.Second), wait: time.Minute, poll: time.Second}
	report, code := runObsDeploymentVerification(t.Context(), options, func(context.Context) obsDeploymentSnapshot { return snapshot }, func() time.Time { return now }, func(context.Context, time.Duration) error { t.Fatal("advisory history added a poll"); return nil }, func(obsDeploymentReport) error { return nil })
	if code != 0 || report.State != "passed" || report.Attempts != 1 || report.HadConfirmedFaults || !report.Timeline.ProbeNotifications.HadSubmissionFailures {
		t.Fatal("direct notification failure changed the original three checks")
	}
}

func TestObsDeploymentNotificationHistoryRequiresLaterCompletedSuccess(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	failed := now.Add(-time.Hour)
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	failure := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", failed)
	recorder.observe(&failure, now, 1)
	for i, completed := range []time.Time{failed.Add(-time.Second), failed, failed.Add(time.Nanosecond)} {
		success := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_succeeded", completed)
		recorder.observe(&success, now, i+2)
		history := recorder.snapshot()
		want := 1
		if i == 2 {
			want = 0
		}
		if history.UnresolvedTrackedFailures != want {
			t.Fatal("equal or earlier success clocks closed a later observed failure")
		}
	}
	last := recorder.snapshot().Events[len(recorder.snapshot().Events)-1]
	if !last.LaterSuccessAfterFailure || !last.LastAttemptAt.Equal(failed.Add(time.Nanosecond)) {
		t.Fatal("submillisecond later completion was lost")
	}
	before := len(recorder.snapshot().Events)
	success := deploymentNotificationHistoryFixture(now.Add(time.Second), "fsn-2", "last_submission_succeeded", now)
	recorder.observe(&success, now.Add(time.Second), 5)
	// One event is allowed when the completion first crosses the evidence boundary.
	if len(recorder.snapshot().Events) != before+1 {
		t.Fatal("crossing the evidence boundary was not recorded")
	}
	success = deploymentNotificationHistoryFixture(now.Add(2*time.Second), "fsn-2", "last_submission_succeeded", now.Add(time.Second))
	recorder.observe(&success, now.Add(2*time.Second), 6)
	if len(recorder.snapshot().Events) != before+1 {
		t.Fatal("repeated successes filled history without changing outcome")
	}
}

func TestObsDeploymentNotificationHistoryOwnsSourceClocks(t *testing.T) {
	now := time.Now().UTC()
	completed := now.Add(-time.Hour)
	serving := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", completed)
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	recorder.observe(&serving, now, 1)
	first := recorder.snapshot()
	*serving.ProbeNotifications.Channels[0].LastAttemptAt = now
	*serving.ProbeNotifications.Channels[0].EvaluatedAt = now.Add(time.Hour)
	if !first.Events[0].LastAttemptAt.Equal(completed) || !first.Events[0].EvaluatedAt.Equal(now) || !first.Events[0].LatestObservedFailureAt.Equal(completed) {
		t.Fatal("source pointers rewrote retained notification history")
	}
}

func TestObsDeploymentNotificationHistoryCountBytesAndImmutableRollover(t *testing.T) {
	for _, longNames := range []bool{false, true} {
		now := time.Now().UTC()
		recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
		var first *obsDeploymentNotificationHistory
		count := api.ObsPostDeployNotificationEventsMax + 100
		for i := range count {
			node := "n"
			if longNames {
				node = strings.Repeat("n", api.ObsServingProbeNodeMaxBytes)
			}
			item := api.ObsServingProbeNotification{Node: node, Channel: "webhook", State: "unobserved", Reason: "not_in_preview"}
			if i%2 != 0 {
				item.Reason = "serving_snapshot_unavailable"
			}
			if longNames {
				run, attempt := now.Add(-time.Second), now.Add(-time.Hour).Add(time.Duration(i)*time.Nanosecond)
				on := true
				item.State, item.Reason, item.Enabled, item.FailureCode = "last_submission_failed", "", &on, "http_status"
				item.EvaluatedAt, item.LastRunAt, item.LastAttemptAt, item.LastSuccessAt = &now, &run, &attempt, &attempt
			}
			recorder.record(item, now.Add(time.Duration(i)*time.Second), i+1)
			if i == 0 {
				first = recorder.snapshot()
			}
		}
		history := recorder.snapshot()
		if !history.Truncated || history.DroppedEvents+len(history.Events) != count || len(history.Events) > api.ObsPostDeployNotificationEventsMax || history.RetainedBytes > api.ObsPostDeployNotificationHistoryBytesMax || history.Events[len(history.Events)-1].Attempt != count {
			t.Fatalf("history exceeded retention or lost recent events: %+v", history)
		}
		if !longNames && len(history.Events) != api.ObsPostDeployNotificationEventsMax || longNames && len(history.Events) >= api.ObsPostDeployNotificationEventsMax {
			t.Fatal("count and byte budgets were not independently exercised")
		}
		charged := 0
		for _, event := range history.Events {
			charged += obsDeploymentNotificationEventBytes(event)
		}
		if charged != history.RetainedBytes || first.Truncated || len(first.Events) != 1 || first.Events[0].Attempt != 1 {
			t.Fatal("byte accounting or an earlier snapshot changed after rollover")
		}
		if longNames && (!history.HadSubmissionFailures || history.UnresolvedTrackedFailures != 1) {
			t.Fatal("event eviction cleared a tracked failure")
		}
	}
}

func deploymentNotificationHistoryFleet(now time.Time, nodes []string) api.ObsServingHealth {
	out := deploymentNotificationHistoryFixture(now, nodes[0], "disabled", time.Time{})
	template := out.Probes[0]
	out.Probes = nil
	for _, node := range nodes {
		probe := template
		probe.Node = node
		out.Probes = append(out.Probes, probe)
	}
	expectedPairs, channels := len(nodes), 2*len(nodes)
	out.Inventory.ExpectedPairs = &expectedPairs
	counts := operatorobs.ServingProbeCounts(out.Probes)
	out.Counts = &counts
	out.State, out.Reason = operatorobs.ServingProbeOverall(counts)
	d := out.ProbeNotifications
	d.ExpectedChannels = &channels
	d.Counts = &api.ObsServingProbeNotificationCounts{Disabled: channels}
	d.Channels = nil
	off := false
	run := now.Add(-time.Second)
	for _, node := range nodes {
		for _, channel := range []string{"heartbeat", "webhook"} {
			d.Channels = append(d.Channels, api.ObsServingProbeNotification{Node: node, Channel: channel, State: "disabled", Enabled: &off, EvaluatedAt: &now, LastRunAt: &run})
		}
	}
	return out
}

func TestObsDeploymentNotificationHistoryTrackingBoundAndDropDisclosure(t *testing.T) {
	now := time.Now().UTC()
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	batch := api.ObsServingProbeNotificationItemsMax
	observed := 0
	for base := 0; base < api.ObsServingProbeNotificationChannelsMax+batch; base += batch {
		nodes := []string{}
		for j := range batch / 2 {
			nodes = append(nodes, fmt.Sprintf("node-%04d", (base/2)+j))
		}
		serving := deploymentNotificationHistoryFleet(now, nodes)
		if !validObsServingHealth(serving) {
			t.Fatal("invalid tracking-bound fixture")
		}
		recorder.observe(&serving, now, base/batch+1)
		observed += batch
	}
	history := recorder.snapshot()
	if history.TrackedChannels != api.ObsServingProbeNotificationChannelsMax || history.DroppedChannelObservations != observed-api.ObsServingProbeNotificationChannelsMax || !history.Truncated || len(recorder.channels) != history.TrackedChannels {
		t.Fatal("tracking grew without a bound or omitted its dropped observations")
	}
}

func TestObsDeploymentNotificationHistoryPreviewOmissionDoesNotRecover(t *testing.T) {
	now := time.Now().UTC()
	recorder := newObsDeploymentNotificationRecorder(now.Add(-90 * time.Second))
	initial := deploymentNotificationHistoryFixture(now, "fsn-2", "last_submission_failed", now.Add(-time.Hour))
	recorder.observe(&initial, now, 1)
	nodes := []string{"fsn-2"}
	for i := 1; i <= api.ObsServingProbeNotificationItemsMax/2; i++ {
		nodes = append(nodes, fmt.Sprintf("node-%04d", i))
	}
	serving := deploymentNotificationHistoryFleet(now, nodes)
	d := serving.ProbeNotifications
	d.Channels = slices.Clone(d.Channels[2:])
	d.State, d.Counts = "available", &api.ObsServingProbeNotificationCounts{Disabled: 2, LastSubmissionFailed: api.ObsServingProbeNotificationItemsMax}
	d.Omitted, d.Truncated = 2, true
	on := true
	completed := now.Add(-time.Hour)
	for i := range d.Channels {
		d.Channels[i].State, d.Channels[i].Enabled, d.Channels[i].FailureCode, d.Channels[i].LastAttemptAt = "last_submission_failed", &on, "timeout", &completed
	}
	if !validObsServingHealth(serving) {
		t.Fatal("invalid preview-omission fixture")
	}
	recorder.observe(&serving, now, 2)
	history := recorder.snapshot()
	if history.MaxPreviewOmitted != 2 || !history.Truncated || history.IncompleteObservations != 1 {
		t.Fatal("source preview omissions were hidden")
	}
	var last obsDeploymentNotificationEvent
	for _, event := range history.Events {
		if event.Node == "fsn-2" && event.Channel == "webhook" {
			last = event
		}
	}
	if last.State != "unobserved" || last.Reason != "not_in_preview" || !last.UnresolvedObservedFailure || last.LaterSuccessAfterFailure || last.LastSuccessAt != nil {
		t.Fatal("omission from a truncated preview manufactured recovery")
	}
}
