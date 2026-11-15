package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func deploymentNotificationFaultFixture(now time.Time) obsDeploymentSnapshot {
	s := deploymentSnapshotTest(now)
	data := s.notifications.ReceiverHealth
	data.State, data.Reason = "degraded", "receiver_fault"
	group := &data.Groups[0]
	group.State, group.Reason = "degraded", "requests_failing"
	*group.Requests.Value, *group.Failures.Value = 3, 1.5
	return s
}

func notificationFaultsForTest(s obsDeploymentSnapshot, after, now time.Time) ([]obsDeploymentCheck, *obsDeploymentNotifications, map[string]obsDeploymentNotificationFailureSet) {
	checks, _, out := assessObsDeployment(s, after, now)
	return checks, out, obsDeploymentNotificationFailures(checks, out, after, now)
}

func TestObsDeploymentNotificationFailuresClassifyConfirmedCauses(t *testing.T) {
	for _, tc := range []struct {
		name, check, kind, signal, reason string
		mutate                            func(*obsDeploymentSnapshot)
	}{
		{"Alertmanager down", "notification_pipeline", "pipeline_signal", "alertmanager_up", "value_not_one", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["alertmanager_up"].Value = 0 }},
		{"failed reload", "notification_pipeline", "pipeline_signal", "alertmanager_config_reload_successful", "value_not_one", func(s *obsDeploymentSnapshot) {
			*s.notifications.Signals["alertmanager_config_reload_successful"].Value = 0
		}},
		{"dropped notifications", "notification_pipeline", "pipeline_signal", "prometheus_dropped", "positive_counter_increase", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["prometheus_dropped"].Value = 1.5 }},
		{"aggregate failures", "notification_pipeline", "pipeline_signal", "alertmanager_failures", "positive_counter_increase", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["alertmanager_failures"].Value = 2 }},
		{"queue capacity", "notification_pipeline", "pipeline_signal", "prometheus_queue_capacity", "queue_capacity_zero", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["prometheus_queue_capacity"].Value = 0 }},
		{"busy queue", "notification_pipeline", "pipeline_queue", "prometheus_queue_length", "queue_busy", func(s *obsDeploymentSnapshot) { *s.notifications.Signals["prometheus_queue_length"].Value = 801 }},
		{"integration failures", "notification_pipeline", "integration", "failures", "positive_counter_increase", func(s *obsDeploymentSnapshot) { *s.notifications.Integrations[0].Failures.Value = 2 }},
		{"receiver failures", "notification_receivers", "receiver", "failures", "requests_failing", func(s *obsDeploymentSnapshot) { *s = deploymentNotificationFaultFixture(s.notifications.GeneratedAt) }},
		{"receiver telemetry missing", "notification_receivers", "receiver", "collector", "telemetry_missing", func(s *obsDeploymentSnapshot) {
			data := s.notifications.ReceiverHealth
			data.State, data.Reason = "degraded", "receiver_fault"
			group := &data.Groups[0]
			group.State, group.Reason = "degraded", "telemetry_missing"
			group.Requests.State, group.Requests.Reason, group.Requests.Value = "no_samples", "insufficient_samples", nil
		}},
		{"unexpected receiver group", "notification_receivers", "receiver", "collector", "unexpected_group", func(s *obsDeploymentSnapshot) {
			data := s.notifications.ReceiverHealth
			group := data.Groups[0]
			group.Expected = new(bool)
			group.Receiver, group.State, group.Reason = "faas-warn", "degraded", "unexpected_group"
			data.Groups = append(data.Groups, group)
			data.State, data.Reason = "degraded", "receiver_fault"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			s := deploymentSnapshotTest(now)
			tc.mutate(&s)
			_, _, failures := notificationFaultsForTest(s, now.Add(-90*time.Second), now)
			set := failures[tc.check]
			if set.total != 1 || len(set.preview) != 1 || len(set.fingerprint) != 64 {
				t.Fatalf("missing confirmed fault: %+v", failures)
			}
			item := set.preview[0]
			if item.Kind != tc.kind || item.Reason != tc.reason || item.Signals[0].Name != tc.signal || !item.Signals[0].ObservedAt.Equal(now) || item.Signals[0].State != "healthy" {
				t.Fatalf("incorrect source evidence: %+v", item)
			}
			if tc.reason == "queue_busy" && (len(item.Signals) != 2 || *item.Threshold != api.ObsNotificationQueueBusyRatio || *item.Signals[1].Value != 1000) {
				t.Fatal("queue fault lost its capacity or threshold")
			}
			if tc.reason == "telemetry_missing" || tc.reason == "unexpected_group" {
				if len(item.Signals) != 2 || item.Signals[1].Name != "inventory" || item.Expected == nil {
					t.Fatal("coverage fault omitted collection or intent evidence")
				}
			} else if tc.signal == "failures" && item.Signals[0].WindowSeconds != int(api.ObsNotificationWindow.Seconds()) {
				t.Fatal("failure evidence lost its rolling-window scope")
			}
		})
	}
}

func TestObsDeploymentNotificationFailuresFilterUnknownStaleAndUnsafe(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-90 * time.Second)
	for _, mutate := range []func(*obsDeploymentSnapshot){
		func(s *obsDeploymentSnapshot) { s.notificationErr = errors.New("https://private-provider/token") },
		func(s *obsDeploymentSnapshot) { s.notifications.GeneratedAt = now.Add(-3 * time.Minute) },
		func(s *obsDeploymentSnapshot) {
			s.notifications.ReceiverHealth.Groups[0].Receiver = "https://private-receiver/token"
		},
		func(s *obsDeploymentSnapshot) {
			s.notifications.ReceiverHealth.Groups[0].Reason = "private-provider-error"
		},
		func(s *obsDeploymentSnapshot) {
			old := after.Add(-time.Second)
			s.notifications.ReceiverHealth.Groups[0].Failures.ObservedAt = &old
		},
		func(s *obsDeploymentSnapshot) { *s.notifications.ReceiverHealth.Groups[0].Failures.Value = math.NaN() },
		func(s *obsDeploymentSnapshot) { *s.notifications.ReceiverHealth.Groups[0].Failures.Value = math.Inf(1) },
		func(s *obsDeploymentSnapshot) {
			group := &s.notifications.ReceiverHealth.Groups[0]
			group.State, group.Reason = "partial", "incomplete_evidence"
			group.Failures.State, group.Failures.Reason, group.Failures.Value = "no_samples", "insufficient_samples", nil
			data := s.notifications.ReceiverHealth
			data.State, data.Reason = "partial", "incomplete_evidence"
		},
	} {
		s := deploymentNotificationFaultFixture(now)
		mutate(&s)
		_, _, failures := notificationFaultsForTest(s, after, now)
		if len(failures) != 0 {
			t.Fatalf("unconfirmed receiver evidence entered history: %+v", failures)
		}
	}
	// A stale peer does not hide a different fresh confirmed fault.
	s := deploymentNotificationFaultFixture(now)
	*s.notifications.Signals["alertmanager_failures"].Value = 2
	old := after.Add(-time.Second)
	s.notifications.ReceiverHealth.Groups[0].Failures.ObservedAt = &old
	_, _, failures := notificationFaultsForTest(s, after, now)
	if failures["notification_pipeline"].total != 1 || failures["notification_receivers"].total != 0 {
		t.Fatal("unknown peer hid or manufactured confirmed evidence")
	}
}

func TestObsDeploymentNotificationFailuresHistoryAndSemanticChanges(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	now, calls := start, 0
	var saved []obsDeploymentReport
	opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: time.Minute, poll: time.Second}
	report, code := runObsDeploymentVerification(context.Background(), opts, func(context.Context) obsDeploymentSnapshot {
		calls++
		s := deploymentNotificationFaultFixture(now)
		*s.notifications.Signals["alertmanager_failures"].Value = float64(calls)
		if calls >= 3 {
			s.notifications.ReceiverHealth.Groups[0].Receiver = "faas-warn"
			s.notifications.Signals["alertmanager_failures"] = deploymentSnapshotTest(now).notifications.Signals["alertmanager_failures"]
			*s.notifications.Signals["prometheus_dropped"].Value = 1
		}
		if calls == 4 {
			s.notificationErr = errors.New("private-provider-error")
		}
		if calls == 5 {
			s = deploymentSnapshotTest(now)
		}
		return s
	}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }, func(r obsDeploymentReport) error { saved = append(saved, r); return nil })
	if code != 0 || !report.HadConfirmedFaults || report.Attempts != 5 || report.State != "passed" {
		t.Fatalf("retention changed qualification: code=%d report=%+v", code, report)
	}
	for _, check := range []string{"notification_pipeline", "notification_receivers"} {
		var transitions []obsDeploymentTransition
		for _, transition := range report.Timeline.Transitions {
			if transition.Check == check {
				transitions = append(transitions, transition)
			}
		}
		if len(transitions) != 4 || transitions[0].Attempt != 1 || transitions[1].Attempt != 3 || transitions[2].State != "unknown" || transitions[3].State != "passed" || transitions[2].NotificationFailures != nil || transitions[3].NotificationFailures != nil {
			t.Fatalf("duplicate clocks/values or lost state transitions: %+v", transitions)
		}
		first, second := transitions[0].NotificationFailures, transitions[1].NotificationFailures
		if first == nil || second == nil || first.CauseSHA256 == second.CauseSHA256 || !first.Items[0].Signals[0].ObservedAt.Equal(start) {
			t.Fatal("later recovery or unavailable evidence erased original causes")
		}
	}
	first := saved[1].Timeline.NotificationEvidence
	if first.RetainedItems != 2 || report.Timeline.NotificationEvidence.RetainedItems != 4 || first.Truncated {
		t.Fatal("later polling changed a previous retention summary")
	}
	var human strings.Builder
	writeObsDeploymentHuman(&human, report)
	for _, want := range []string{"notification_evidence retained_items=4", "receiver=faas-page integration=slack", "receiver=faas-warn integration=slack", "name=alertmanager_failures", "name=prometheus_dropped", "window_seconds=600"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human history omitted %q", want)
		}
	}
	body, err := json.Marshal(report)
	if err != nil || strings.Contains(string(body)+human.String(), "private-provider-error") {
		t.Fatal("history leaked provider error text")
	}
}

func TestObsDeploymentNotificationFailuresOwnSourcesAndStableOrder(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentNotificationFaultFixture(now)
	*s.notifications.Integrations[0].Failures.Value = 2
	checks, projected, failures := notificationFaultsForTest(s, now.Add(-90*time.Second), now)
	recorder := newObsDeploymentTimeline(checks)
	history := recorder.observeWithFaults(checks, now, 1, obsDeploymentProbeFailureSet{}, failures)
	before, _ := json.Marshal(history)
	*projected.ReceiverHealth.Groups[0].Failures.Value = 99
	*projected.ReceiverHealth.Groups[0].Failures.ObservedAt = now.Add(time.Hour)
	*projected.ReceiverHealth.Groups[0].Expected = false
	for _, set := range failures {
		*set.preview[0].Signals[0].Value = 123
		*set.preview[0].Signals[0].ObservedAt = now.Add(time.Hour)
	}
	after, _ := json.Marshal(history)
	if string(before) != string(after) {
		t.Fatal("source or extraction mutation rewrote retained history")
	}
	items := []obsDeploymentNotificationFailure{{Kind: "integration", Integration: "slack", Reason: "positive_counter_increase"}, {Kind: "integration", Integration: "email", Reason: "positive_counter_increase"}}
	first := obsDeploymentNotificationFailureFingerprint(slices.Clone(items))
	slices.Reverse(items)
	second := obsDeploymentNotificationFailureFingerprint(items)
	if first.fingerprint != second.fingerprint || !reflect.DeepEqual(first.preview, second.preview) {
		t.Fatal("input ordering changed semantic identity")
	}
}
