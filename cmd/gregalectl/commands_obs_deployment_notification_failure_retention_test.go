package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func deploymentNotificationManyFaultsFixture(now time.Time, groups int) obsDeploymentSnapshot {
	s := deploymentNotificationFaultFixture(now)
	names := operatorobs.NotificationIntegrationNames()
	slices.Sort(names)
	integration := s.notifications.Integrations[0]
	*s.notifications.Integrations[0].Failures.Value = 1.5
	s.notifications.Integrations = nil
	for _, name := range names {
		copy := integration
		copy.Integration = name
		s.notifications.Integrations = append(s.notifications.Integrations, copy)
	}
	data := s.notifications.ReceiverHealth
	group := data.Groups[0]
	data.Groups = nil
	for _, receiver := range operatorobs.NotificationReceiverNames() {
		for _, name := range names {
			if len(data.Groups) == groups {
				break
			}
			copy := group
			copy.Receiver, copy.Integration = receiver, name
			data.Groups = append(data.Groups, copy)
		}
	}
	count := float64(len(data.Groups))
	data.Inventory.Value = &count
	return s
}

func TestObsDeploymentNotificationFailuresChangesOutsidePreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentNotificationManyFaultsFixture(now, 10)
	checks, _, first := notificationFaultsForTest(s, now.Add(-90*time.Second), now)
	recorder := newObsDeploymentTimeline(checks)
	recorder.observeWithFaults(checks, now, 1, obsDeploymentProbeFailureSet{}, first)
	group := &s.notifications.ReceiverHealth.Groups[9]
	group.Reason = "telemetry_missing"
	group.Failures.State, group.Failures.Reason, group.Failures.Value = "no_samples", "insufficient_samples", nil
	_, _, next := notificationFaultsForTest(s, now.Add(-90*time.Second), now)
	history := recorder.observeWithFaults(checks, now, 2, obsDeploymentProbeFailureSet{}, next)
	if first["notification_receivers"].fingerprint == next["notification_receivers"].fingerprint || len(history.Transitions) != 4 {
		t.Fatal("cause change outside retained preview did not add a transition")
	}
	capture := history.Transitions[3].NotificationFailures
	if capture == nil || capture.Total != 10 || len(capture.Items) != api.ObsPostDeployNotificationEvidencePerTransitionMax || capture.Omitted != 6 || !capture.Truncated || capture.Items[0].Reason != "requests_failing" {
		t.Fatalf("incomplete preview was not disclosed: %+v", capture)
	}
}

func TestObsDeploymentNotificationFailuresBudgetsAndRollover(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	check := obsDeploymentCheck{Name: "notification_receivers", State: "degraded", Reason: "receiver_requests_failing"}
	recorder := newObsDeploymentTimeline([]obsDeploymentCheck{check})
	var first, history *obsDeploymentTimeline
	for attempt := range api.ObsPostDeployTransitionsMax + 90 {
		now := start.Add(time.Duration(attempt) * time.Second)
		s := deploymentNotificationManyFaultsFixture(now, api.ObsNotificationIntegrationsMax)
		checks, _, faults := notificationFaultsForTest(s, start.Add(-90*time.Second), now)
		if attempt%2 != 0 {
			checks[2].State, checks[2].Reason = "unknown", "source_unavailable"
		}
		history = recorder.observeWithFaults(checks, now, attempt+1, obsDeploymentProbeFailureSet{}, faults)
		if attempt == 0 {
			first = history
		}
		retention := history.NotificationEvidence
		items, bytes := 0, 0
		for _, transition := range history.Transitions {
			if capture := transition.NotificationFailures; capture != nil {
				items += len(capture.Items)
				bytes += obsDeploymentNotificationEvidenceBytes(transition, capture)
			}
		}
		if retention.RetainedItems != items || retention.RetainedBytes != bytes || bytes > api.ObsPostDeployNotificationEvidenceBytesMax || items > api.ObsPostDeployNotificationEvidenceItemsMax {
			t.Fatalf("retention budget or accounting mismatch: %+v items=%d bytes=%d", retention, items, bytes)
		}
	}
	retention := history.NotificationEvidence
	if history.DroppedTransitions != 90 || !retention.Truncated || retention.RetainedItems <= 0 || retention.DroppedItems == 0 || retention.DroppedSnapshots == 0 || first.NotificationEvidence.DroppedItems != api.ObsNotificationIntegrationsMax-api.ObsPostDeployNotificationEvidencePerTransitionMax {
		t.Fatalf("caps, rollover or previous snapshot were lost: %+v first=%+v", retention, first.NotificationEvidence)
	}
	if history.ProbeEvidence.RetainedItems != 0 || history.ProbeEvidence.RetainedBytes != 0 || history.ProbeEvidence.DroppedItems != 0 {
		t.Fatal("notification retention consumed serving fault history")
	}
	// The measured budget includes all added property bytes, not just item JSON.
	full := *history
	without := *history
	without.Transitions = slices.Clone(history.Transitions)
	for i := range without.Transitions {
		without.Transitions[i].NotificationFailures = nil
	}
	withBody, _ := json.MarshalIndent(full, "", "  ")
	withoutBody, _ := json.MarshalIndent(without, "", "  ")
	if len(withBody)-len(withoutBody) > retention.RetainedBytes {
		t.Fatal("retention undercharged indented report properties")
	}
}

func TestObsDeploymentNotificationFailuresTerminalPaths(t *testing.T) {
	for _, mode := range []string{"timeout", "lost", "cancel", "write_failure"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			now := start
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			calls, writes := 0, 0
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				s := deploymentNotificationFaultFixture(now)
				if mode == "lost" && calls > 1 {
					s.notificationErr = errors.New("private-provider-text")
				}
				return s
			}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
				if mode == "cancel" {
					cancel()
					return ctx.Err()
				}
				now = now.Add(d)
				return nil
			}, func(obsDeploymentReport) error {
				writes++
				if mode == "write_failure" && writes == 2 {
					return errors.New("private-write-error")
				}
				return nil
			})
			want := 3
			if mode == "timeout" {
				want = 1
			} else if mode == "write_failure" {
				want = 2
			}
			if code != want || !report.HadConfirmedFaults || report.Timeline.NotificationEvidence.RetainedItems != 1 {
				t.Fatalf("terminal state changed fault qualification or lost evidence: %d %+v", code, report)
			}
			capture := report.Timeline.Transitions[2].NotificationFailures
			if capture == nil || capture.Items[0].Receiver != "faas-page" || !capture.Items[0].Signals[0].ObservedAt.Equal(start) {
				t.Fatal("terminal result lost or re-stamped the original cause")
			}
			body, err := json.Marshal(report)
			if err != nil || strings.Contains(string(body), "private-") {
				t.Fatal("terminal history leaked source error text")
			}
		})
	}
}
