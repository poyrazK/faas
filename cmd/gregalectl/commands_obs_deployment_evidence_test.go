package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func qualificationEvidenceForTest(s obsDeploymentSnapshot, after, now time.Time) (*obsDeploymentEvidence, []obsDeploymentCheck) {
	checks, serving, notifications := assessObsDeployment(s, after, now)
	r := newObsDeploymentEvidence(checks)
	return r.observe(checks, s, serving, notifications, after, now), checks
}

func TestObsDeploymentQualificationEvidenceSpecificBlockers(t *testing.T) {
	for _, tc := range []struct {
		name, kind, reason string
		check              int
		mutate             func(*obsDeploymentSnapshot, time.Time)
	}{
		{"probe before deployment", "probe_run", "before_deployment_boundary", 0, func(s *obsDeploymentSnapshot, now time.Time) {
			at := now.Add(-91 * time.Second)
			s.serving.value.Probes[0].LastRunAt = &at
		}},
		{"missing probe evaluation", "probe_evaluation", "missing_timestamp", 0, func(s *obsDeploymentSnapshot, _ time.Time) {
			deploymentProbeTestState(s, "unknown", "query_failed")
			p := &s.serving.value.Probes[0]
			p.Reporting, p.EvaluatedAt, p.LastRunAt = nil, nil, nil
			qualificationTestCounts(s)
		}},
		{"unknown probe state", "probe_evaluation", "unknown_probe_state", 0, func(s *obsDeploymentSnapshot, _ time.Time) {
			deploymentProbeTestState(s, "unknown", "query_failed")
			s.serving.value.Probes[0].Reporting = nil
			qualificationTestCounts(s)
		}},
		{"serving inventory before deployment", "serving_inventory", "before_deployment_boundary", 0, func(s *obsDeploymentSnapshot, now time.Time) {
			at := now.Add(-91 * time.Second)
			s.serving.value.Inventory.ObservedAt = &at
		}},
		{"inventory not configured", "serving_inventory", "expectations_not_configured", 0, func(s *obsDeploymentSnapshot, _ time.Time) {
			out := &s.serving.value
			configured, count := false, 0
			out.Inventory.Configured, out.Inventory.ExpectedPairs = &configured, &count
			out.Counts, out.Probes, out.State, out.Reason = nil, []api.ObsServingProbe{}, "partial", "inventory_not_configured"
		}},
		{"missing pipeline metric", "pipeline_signal", "missing_signal", 1, func(s *obsDeploymentSnapshot, _ time.Time) {
			delete(s.notifications.Signals, "prometheus_queue_length")
		}},
		{"missing metric timestamp", "pipeline_signal", "missing_timestamp", 1, func(s *obsDeploymentSnapshot, _ time.Time) {
			signal := s.notifications.Signals["prometheus_queue_length"]
			signal.ObservedAt = nil
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"metric before deployment", "pipeline_signal", "before_deployment_boundary", 1, func(s *obsDeploymentSnapshot, now time.Time) {
			signal := s.notifications.Signals["prometheus_queue_length"]
			at := now.Add(-91 * time.Second)
			signal.ObservedAt = &at
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"future metric clock", "pipeline_signal", "future_timestamp", 1, func(s *obsDeploymentSnapshot, now time.Time) {
			signal := s.notifications.Signals["prometheus_queue_length"]
			at := now.Add(31 * time.Second)
			signal.ObservedAt = &at
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"pipeline metric not sampled", "pipeline_signal", "missing_samples", 1, func(s *obsDeploymentSnapshot, _ time.Time) {
			signal := s.notifications.Signals["prometheus_queue_length"]
			signal.State, signal.Reason, signal.Value, signal.ObservedAt = "no_samples", "insufficient_samples", nil, nil
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"pipeline query unavailable", "pipeline_signal", "signal_unavailable", 1, func(s *obsDeploymentSnapshot, _ time.Time) {
			signal := s.notifications.Signals["prometheus_queue_length"]
			signal.State, signal.Reason, signal.Value, signal.ObservedAt = "unavailable", "query_failed", nil, nil
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"unsupported window", "pipeline_metadata", "unexpected_window", 1, func(s *obsDeploymentSnapshot, _ time.Time) { s.notifications.WindowSeconds++ }},
		{"missing integration expectations", "integration_inventory", "expectations_unavailable", 1, func(s *obsDeploymentSnapshot, _ time.Time) { s.notifications.Integrations = nil }},
		{"missing receiver section", "receiver_metadata", "receiver_evidence_missing", 2, func(s *obsDeploymentSnapshot, _ time.Time) { s.notifications.ReceiverHealth = nil }},
		{"receiver collection not ready", "receiver_collector", "collector_not_ready", 2, func(s *obsDeploymentSnapshot, _ time.Time) {
			d := s.notifications.ReceiverHealth
			*d.Collector.Value = 0
			d.State, d.Reason = "partial", "collector_unavailable"
			d.Groups[0].State, d.Groups[0].Reason = "partial", "collector_unavailable"
		}},
		{"receiver collection before deployment", "receiver_collector", "before_deployment_boundary", 2, func(s *obsDeploymentSnapshot, now time.Time) {
			at := now.Add(-91 * time.Second)
			s.notifications.ReceiverHealth.Collector.ObservedAt = &at
		}},
		{"quiet receiver not sampled", "receiver_success_observation", "missing_samples", 2, func(s *obsDeploymentSnapshot, _ time.Time) {
			d := s.notifications.ReceiverHealth
			g := &d.Groups[0]
			d.State, d.Reason, g.State, g.Reason = "partial", "incomplete_evidence", "partial", "incomplete_evidence"
			g.LastSuccessObservation.State, g.LastSuccessObservation.Reason, g.LastSuccessObservation.Value, g.LastSuccessObservation.ObservedAt = "no_samples", "success_not_observed", nil, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			s := deploymentSnapshotTest(now)
			tc.mutate(&s, now)
			evidence, checks := qualificationEvidenceForTest(s, now.Add(-90*time.Second), now)
			if checks[tc.check].State != "unknown" || evidence.Checks[tc.check].State != "blocked" {
				t.Fatalf("fixture did not produce readable unknown evidence: checks=%+v evidence=%+v", checks, evidence.Checks[tc.check])
			}
			preview := evidence.Checks[tc.check].Blockers
			if preview == nil || preview.Total != 1 || len(preview.Items) != 1 || preview.Items[0].Kind != tc.kind || preview.Items[0].Reason != tc.reason || preview.Truncated {
				t.Fatalf("incorrect blocker: %+v", preview)
			}
			item := preview.Items[0]
			if tc.kind == "probe_run" && (item.Node != "fsn-2" || len(item.URLID) != 64 || *item.AgeSeconds != 91 || item.MaxAgeSeconds != 900) {
				t.Fatal("probe blocker lost identity or age policy")
			}
			if tc.kind == "receiver_success_observation" && (item.Receiver != "faas-page" || item.Integration != "slack" || item.ObservedAt != nil || item.AgeSeconds != nil) {
				t.Fatal("missing receiver observations became zero clocks")
			}
			body, err := json.Marshal(evidence)
			if err != nil || strings.Contains(string(body), `"value"`) {
				t.Fatal("blocker diagnostics copied metric or historical success values")
			}
		})
	}
}

func TestObsDeploymentQualificationEvidenceStaleAndUpstreamClocks(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-10 * time.Minute)
	for _, mode := range []string{"snapshot", "probe evaluation", "pipeline metric", "integration observation", "receiver observation"} {
		t.Run(mode, func(t *testing.T) {
			s := deploymentSnapshotTest(now)
			index, kind := 1, "pipeline_signal"
			old := now.Add(-3 * time.Minute)
			switch mode {
			case "snapshot":
				s = deploymentSnapshotTest(old)
				index, kind = 0, "serving_snapshot"
			case "probe evaluation":
				s = deploymentSnapshotTest(now.Add(-time.Minute))
				s.serving.value.Probes[0].EvaluatedAt = &old
				index, kind = 0, "probe_evaluation"
			case "pipeline metric":
				signal := s.notifications.Signals["prometheus_queue_length"]
				signal.ObservedAt = &old
				s.notifications.Signals["prometheus_queue_length"] = signal
			case "integration observation":
				s.notifications.Integrations[0].Attempts.ObservedAt = &old
				kind = "integration_signal"
			case "receiver observation":
				s = deploymentSnapshotTest(now.Add(-time.Minute))
				s.notifications.ReceiverHealth.Groups[0].LastSuccessObservation.ObservedAt = &old
				index, kind = 2, "receiver_success_observation"
			}
			evidence, checks := qualificationEvidenceForTest(s, after, now)
			preview := evidence.Checks[index].Blockers
			if checks[index].State != "unknown" || preview == nil {
				t.Fatalf("stale fixture was not readable: %+v", checks)
			}
			found := false
			for _, item := range preview.Items {
				if item.Kind == kind && item.Reason == "stale" && *item.AgeSeconds == 180 && item.MaxAgeSeconds == 120 && item.ObservedAt.Equal(old) {
					found = true
				}
				if mode == "snapshot" && strings.HasPrefix(item.Kind, "probe_") {
					t.Fatal("unusable parent snapshot emitted downstream probe claims")
				}
			}
			if !found {
				t.Fatalf("stale source clock was lost in projection: %+v", preview.Items)
			}
		})
	}
}

func TestObsDeploymentQualificationEvidenceClockBoundaries(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	after := now.Add(-time.Minute)
	for _, at := range []time.Time{after, after.Add(-time.Nanosecond), now.Add(api.ObsHealthClockSkewTolerance), now.Add(api.ObsHealthClockSkewTolerance + time.Nanosecond), now.Add(-api.ObsHealthFreshnessMaxAge), now.Add(-api.ObsHealthFreshnessMaxAge - time.Nanosecond)} {
		item := obsDeploymentEvidenceClock("pipeline_signal", &at, api.ObsHealthFreshnessMaxAge.Seconds(), false, after, now)
		if (item.Reason == "") != obsDeploymentFresh(&at, after, now) {
			t.Fatalf("diagnostics disagreed with qualification at %s: %+v", at, item)
		}
	}
	at := now.Add(-900*time.Second - 400*time.Microsecond)
	maxAge := 900.0009
	item := obsDeploymentEvidenceClock("probe_run", &at, maxAge, true, now.Add(-time.Hour), now)
	if (item.Reason == "") != obsDeploymentFreshRun(&at, now.Add(-time.Hour), now, maxAge) || item.Reason != "stale" || *item.AgeSeconds != 900.001 {
		t.Fatal("probe diagnostics did not honor millisecond policy comparisons")
	}
}

func TestObsDeploymentQualificationEvidenceRedaction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"probe identity", "metric reason", "integration label", "receiver label", "nonfinite value"} {
		t.Run(mode, func(t *testing.T) {
			s := deploymentSnapshotTest(now)
			switch mode {
			case "probe identity":
				s.serving.value.Probes[0].Node = "https://private/token"
			case "metric reason":
				signal := s.notifications.Signals["prometheus_queue_length"]
				signal.Reason = "https://private/token"
				s.notifications.Signals["prometheus_queue_length"] = signal
			case "integration label":
				s.notifications.Integrations[0].Integration = "https://private/token"
			case "receiver label":
				s.notifications.ReceiverHealth.Groups[0].Receiver = "https://private/token"
			case "nonfinite value":
				*s.notifications.Signals["prometheus_queue_length"].Value = math.Inf(1)
			}
			evidence, checks := qualificationEvidenceForTest(s, now.Add(-90*time.Second), now)
			if state, _ := obsDeploymentOverall(checks); state != "unknown" {
				t.Fatal("unusable metadata became healthy or confirmed faulty")
			}
			body, err := json.Marshal(evidence)
			if err != nil || strings.Contains(string(body), "private") || strings.Contains(string(body), "token") || strings.Contains(string(body), "+Inf") {
				t.Fatal("unreviewed metadata entered qualification diagnostics")
			}
		})
	}
}

func qualificationTestCounts(s *obsDeploymentSnapshot) {
	counts := operatorobs.ServingProbeCounts(s.serving.value.Probes)
	s.serving.value.Counts = &counts
	s.serving.value.State, s.serving.value.Reason = operatorobs.ServingProbeOverall(counts)
}
