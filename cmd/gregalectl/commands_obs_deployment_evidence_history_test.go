package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func blockedQualificationSnapshot(now, after time.Time) obsDeploymentSnapshot {
	s := deploymentSnapshotTest(now)
	run := after.Add(-time.Second)
	s.serving.value.Probes[0].LastRunAt = &run
	delete(s.notifications.Signals, "prometheus_queue_length")
	d := s.notifications.ReceiverHealth
	d.State, d.Reason, d.Groups[0].State, d.Groups[0].Reason = "partial", "incomplete_evidence", "partial", "incomplete_evidence"
	success := &d.Groups[0].LastSuccessObservation
	success.State, success.Reason, success.Value, success.ObservedAt = "no_samples", "success_not_observed", nil, nil
	return s
}

func TestObsDeploymentQualificationEvidenceHistoryAndTerminalPaths(t *testing.T) {
	for _, mode := range []string{"timeout", "recovery", "cancel", "write error"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			now, calls := start, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			opts := obsDeploymentOptions{after: start.Add(-90 * time.Second), wait: 2 * time.Second, poll: time.Second}
			if mode == "recovery" {
				opts.wait, opts.stableFor = 5*time.Second, time.Second
			}
			var saved []obsDeploymentReport
			report, code := runObsDeploymentVerification(ctx, opts, func(context.Context) obsDeploymentSnapshot {
				calls++
				s := blockedQualificationSnapshot(now, opts.after)
				if calls > 2 {
					if mode == "recovery" {
						return deploymentSnapshotTest(now)
					}
					s.serving.err, s.notificationErr = errors.New("private-source-error"), errors.New("private-provider-error")
				}
				return s
			}, func() time.Time { return now }, func(_ context.Context, d time.Duration) error {
				if mode == "cancel" && calls == 2 {
					cancel()
					return ctx.Err()
				}
				now = now.Add(d)
				return nil
			}, func(r obsDeploymentReport) error {
				saved = append(saved, r)
				if mode == "write error" && r.Attempts == 2 {
					return errors.New("private-write-error")
				}
				return nil
			})
			want := 3
			if mode == "recovery" {
				want = 0
			}
			if mode == "write error" {
				want = 2
			}
			if code != want || report.HadConfirmedFaults {
				t.Fatalf("diagnostics changed qualification: code=%d report=%+v", code, report)
			}
			initial := saved[0].QualificationEvidence
			if initial == nil || initial.ObservedAt != nil || len(initial.Checks) != 3 {
				t.Fatal("initial evidence was not unassessed")
			}
			if initial.History == nil || initial.History.TotalEvents != 0 || len(initial.History.Events) != 0 {
				t.Fatal("initial report invented progress events")
			}
			for _, check := range initial.Checks {
				if check.State != "not_observed" || check.Blockers != nil || check.LastBlocked != nil {
					t.Fatal("initial report invented evidence")
				}
				if check.Progress == nil || check.Progress.FirstObservedAt != nil || check.Progress.FirstBlockedAt != nil || check.Progress.FirstPassedAt != nil || check.Progress.ObservedBlockedSeconds != 0 || check.Progress.ObservedUnavailableSeconds != 0 {
					t.Fatal("initial report invented qualification timing")
				}
			}
			if saved[1].QualificationEvidence.Checks[0].Blockers.Items[0].Reason != "before_deployment_boundary" {
				t.Fatal("first readable blocker was not saved")
			}
			previous := saved[2].QualificationEvidence
			for i, check := range report.QualificationEvidence.Checks {
				blockedSeconds := 1.0
				if mode == "timeout" || mode == "recovery" {
					blockedSeconds = 2
				}
				if p := check.Progress; !p.FirstBlockedAt.Equal(start) || p.ObservedBlockedSeconds != blockedSeconds || p.ObservedUnavailableSeconds != 0 || mode == "recovery" && !p.FirstPassedAt.Equal(start.Add(2*time.Second)) || mode != "recovery" && p.FirstPassedAt != nil {
					t.Fatal("terminal handling advanced or discarded sampled waiting time")
				}
				if mode == "timeout" || mode == "recovery" {
					if check.LastBlocked == nil || !check.LastBlocked.At.Equal(start.Add(time.Second)) || !reflect.DeepEqual(check.LastBlocked.Blockers, previous.Checks[i].Blockers) || check.Blockers != nil {
						t.Fatal("terminal read or recovery hid or re-stamped readable blockers")
					}
					if mode == "timeout" && check.State != "unavailable" || mode == "recovery" && check.State != "passed" {
						t.Fatal("historical blockers were presented as current")
					}
				} else if !reflect.DeepEqual(report.QualificationEvidence, previous) {
					t.Fatal("terminal save invented an evidence assessment")
				}
			}
			body, err := json.Marshal(report.QualificationEvidence)
			if err != nil || strings.Contains(string(body), "private") {
				t.Fatal("unreviewed source text entered qualification history")
			}
			var human strings.Builder
			writeObsDeploymentHuman(&human, report)
			if !strings.Contains(human.String(), "qualification_evidence observed_at=") || !strings.Contains(human.String(), "kind=probe_run") {
				t.Fatal("human output omitted qualification blockers")
			}
			if mode == "recovery" && (calls != 4 || report.Stability.State != "passed" || report.Stability.Resets != 0) {
				t.Fatal("pre-window blockers manufactured a stability window or reset")
			}
		})
	}
}

func TestObsDeploymentQualificationEvidenceFullFleetAndBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentSnapshotTest(now)
	probe := s.serving.value.Probes[0]
	s.serving.value.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax)
	for i := range s.serving.value.Probes {
		s.serving.value.Probes[i] = probe
		s.serving.value.Probes[i].URLID = fmt.Sprintf("%064x", i)
	}
	run := now.Add(-91 * time.Second)
	for i := api.ObsServingProbePairsMax - 6; i < api.ObsServingProbePairsMax; i++ {
		s.serving.value.Probes[i].LastRunAt = &run
	}
	expected := len(s.serving.value.Probes)
	s.serving.value.Inventory.ExpectedPairs = &expected
	qualificationTestCounts(&s)
	evidence, checks := qualificationEvidenceForTest(s, now.Add(-90*time.Second), now)
	preview := evidence.Checks[0].Blockers
	if checks[0].State != "unknown" || preview == nil || preview.Total != 6 || preview.Omitted != 2 || !preview.Truncated || len(preview.Items) != api.ObsPostDeployEvidenceItemsMax {
		t.Fatalf("full fleet omissions were hidden: %+v", preview)
	}
	for i, item := range preview.Items {
		if item.URLID != fmt.Sprintf("%064x", 994+i) || item.Reason != "before_deployment_boundary" {
			t.Fatal("preview ordering was unstable or excluded probes outside another preview")
		}
	}
	// Independently exercise the byte cap, not just the bounded item count.
	items := make([]obsDeploymentEvidenceDetail, api.ObsPostDeployEvidenceItemsMax+1)
	for i := range items {
		items[i] = obsDeploymentEvidenceDetail{Kind: "probe_run", Node: strings.Repeat("n", api.ObsPostDeployEvidenceBytesMax/2), Reason: "before_deployment_boundary"}
	}
	preview = retainObsDeploymentEvidence(items)
	if len(preview.Items) == 0 || len(preview.Items) >= api.ObsPostDeployEvidenceItemsMax || !preview.Truncated || preview.Omitted != len(items)-len(preview.Items) || preview.RetainedBytes > preview.ByteLimit {
		t.Fatal("byte budget was not enforced")
	}
	body, err := json.MarshalIndent(preview.Items, "            ", "  ")
	if err != nil || len(body) > preview.RetainedBytes || preview.RetainedBytes != obsDeploymentEvidenceBytes(preview.Items) {
		t.Fatal("pretty report bytes were undercharged")
	}
}

func TestObsDeploymentQualificationEvidenceQuietReceiversAndFaultPrecedence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentSnapshotTest(now)
	evidence, checks := qualificationEvidenceForTest(s, now.Add(-90*time.Second), now)
	for i, check := range checks {
		if check.State != "passed" || evidence.Checks[i].State != "passed" || evidence.Checks[i].Blockers != nil {
			t.Fatal("quiet historical delivery required a new notification")
		}
	}
	s = blockedQualificationSnapshot(now, now.Add(-90*time.Second))
	*s.notifications.Signals["alertmanager_failures"].Value = 1
	evidence, checks = qualificationEvidenceForTest(s, now.Add(-90*time.Second), now)
	if state, _ := obsDeploymentOverall(checks); state != "degraded" || evidence.Checks[0].State != "blocked" || evidence.Checks[1].State != "faulted" || evidence.Checks[1].Blockers != nil {
		t.Fatal("unknown blockers changed confirmed-fault precedence")
	}
}

func maximumQualificationEvidence(now time.Time) *obsDeploymentEvidence {
	clock := now.Add(-6*time.Minute - time.Nanosecond)
	age := 360.123456789
	checks := []obsDeploymentEvidenceCheck{}
	integrations := operatorobs.NotificationIntegrationNames()
	for _, name := range []string{"serving_probes", "notification_pipeline", "notification_receivers"} {
		items := []obsDeploymentEvidenceDetail{}
		for i := range api.ObsPostDeployEvidenceItemsMax {
			item := obsDeploymentEvidenceDetail{Kind: "probe_run", Node: strings.Repeat("n", api.ObsServingProbeNodeMaxBytes-4) + fmt.Sprintf("%04d", i), URLID: fmt.Sprintf("%064x", i), Reason: "before_deployment_boundary", SourceState: "healthy", ObservedAt: &clock, AgeSeconds: &age, MaxAgeSeconds: 900.1234567890123, AllowedClockSkewSeconds: 30}
			if name != "serving_probes" {
				item.Node, item.URLID, item.Integration, item.MaxAgeSeconds = "", "", integrations[i], 120
				item.Kind, item.Name = "integration_signal", "attempts"
				item.Reason = "stale"
				if name == "notification_receivers" {
					item.Kind, item.Name, item.Receiver = "receiver_success_observation", "", "faas-heartbeat"
				}
			}
			items = append(items, item)
		}
		preview := retainObsDeploymentEvidence(items)
		preview.Total, preview.Omitted, preview.Truncated = api.ObsServingProbePairsMax, api.ObsServingProbePairsMax-len(preview.Items), true
		first, blocked, passed := now.Add(-time.Hour), now.Add(-59*time.Minute), now.Add(-time.Second)
		checks = append(checks, obsDeploymentEvidenceCheck{Name: name, State: "passed", LastBlocked: &obsDeploymentEvidenceObservation{At: now.Add(-time.Second), Blockers: preview}, Progress: &obsDeploymentEvidenceProgress{FirstObservedAt: &first, FirstBlockedAt: &blocked, FirstPassedAt: &passed, StateSince: &passed, ObservedBlockedSeconds: 1234.123456789, ObservedUnavailableSeconds: 123.123456789}})
	}
	return &obsDeploymentEvidence{ObservedAt: &now, Checks: checks}
}

func maximumQualificationHistory(now time.Time, checks []obsDeploymentEvidenceCheck) *obsDeploymentEvidenceHistory {
	history := newObsDeploymentEvidenceHistory()
	for i := range 2 * api.ObsPostDeployEvidenceEventsMax {
		check := checks[i%len(checks)]
		history.append(obsDeploymentEvidenceEvent{At: now.Add(time.Duration(i-2*api.ObsPostDeployEvidenceEventsMax) * time.Second), Check: check.Name, State: "blocked", Reason: "awaiting_post_deployment_evidence", CauseSHA256: fmt.Sprintf("%064x", i), Blockers: check.LastBlocked.Blockers})
	}
	return &history
}
