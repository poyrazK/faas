package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func stabilityTestProjection(t *testing.T, s obsDeploymentSnapshot, after, now time.Time) (*api.ObsServingHealth, *obsDeploymentNotifications) {
	t.Helper()
	checks, serving, notifications := assessObsDeployment(s, after, now)
	if state, _ := obsDeploymentOverall(checks); state != "passed" {
		t.Fatalf("invalid passing fixture: %+v", checks)
	}
	return serving, notifications
}

func TestObsDeploymentStabilityRequiresEverySource(t *testing.T) {
	type clockSetter struct {
		name string
		set  func(*obsDeploymentSnapshot, *time.Time)
	}
	clocks := []clockSetter{
		{"serving inventory", func(s *obsDeploymentSnapshot, at *time.Time) { s.serving.value.Inventory.ObservedAt = at }},
		{"probe evaluation", func(s *obsDeploymentSnapshot, at *time.Time) { s.serving.value.Probes[0].EvaluatedAt = at }},
		{"probe run", func(s *obsDeploymentSnapshot, at *time.Time) { s.serving.value.Probes[0].LastRunAt = at }},
		{"alertmanager API", func(s *obsDeploymentSnapshot, at *time.Time) { s.notifications.AlertmanagerAPI.ObservedAt = at }},
		{"integration attempts", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.Integrations[0].Attempts.ObservedAt = at
		}},
		{"integration failures", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.Integrations[0].Failures.ObservedAt = at
		}},
		{"receiver collector", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.ReceiverHealth.Collector.ObservedAt = at
		}},
		{"receiver inventory", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.ReceiverHealth.Inventory.ObservedAt = at
		}},
		{"receiver requests", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.ReceiverHealth.Groups[0].Requests.ObservedAt = at
		}},
		{"receiver failures", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.ReceiverHealth.Groups[0].Failures.ObservedAt = at
		}},
		{"receiver success observation", func(s *obsDeploymentSnapshot, at *time.Time) {
			s.notifications.ReceiverHealth.Groups[0].LastSuccessObservation.ObservedAt = at
		}},
	}
	for _, name := range deploymentSignalNames() {
		clocks = append(clocks, clockSetter{name, func(s *obsDeploymentSnapshot, at *time.Time) {
			signal := s.notifications.Signals[name]
			signal.ObservedAt = at
			s.notifications.Signals[name] = signal
		}})
	}
	for _, clock := range clocks {
		t.Run(clock.name, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			after := start.Add(-90 * time.Second)
			r := newObsDeploymentStability(2 * time.Second)
			initial := deploymentSnapshotTest(start)
			// Normalize the run clock to match the frozen source baseline.
			clock.set(&initial, &start)
			serving, notifications := stabilityTestProjection(t, initial, after, start)
			if r.observe("passed", serving, notifications, start) {
				t.Fatal("first observation proved a window")
			}
			at := start.Add(2 * time.Second)
			latest := deploymentSnapshotTest(at)
			clock.set(&latest, &start)
			serving, notifications = stabilityTestProjection(t, latest, after, at)
			if r.observe("passed", serving, notifications, at) || r.status.ObservedSeconds != 0 || r.status.SourceCount != len(clocks) || r.status.ElapsedSeconds != 2 {
				t.Fatalf("unchanged source proved stability: %+v", r.status)
			}
			preview := r.snapshot().Diagnostics.Blockers
			if preview.Total != 1 || len(preview.Items) != 1 || *preview.Items[0].AdvancedSeconds != 0 || *preview.Items[0].RemainingSeconds != 2 || !preview.Items[0].LatestAt.Equal(start) {
				t.Fatalf("frozen clock was not identified in diagnostics: %+v", preview)
			}
		})
	}
}

func TestObsDeploymentStabilityResetsChangedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		offset       time.Duration
		mutate       func(*obsDeploymentSnapshot, time.Time)
	}{
		{"node", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) { s.serving.value.Probes[0].Node = "fsn-3" }},
		{"URL identity", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) { s.serving.value.Probes[0].URLID = strings.Repeat("f", 64) }},
		{"probe budget", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) { s.serving.value.Probes[0].ExpectedFirstByteSeconds = 2.5 }},
		{"probe age policy", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) { s.serving.value.Probes[0].MaxResultAgeSeconds = 1200 }},
		{"receiver identity", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) {
			s.notifications.ReceiverHealth.Groups[0].Receiver = "faas-warn"
		}},
		{"receiver integration", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) {
			s.notifications.ReceiverHealth.Groups[0].Integration = "email"
		}},
		{"aggregate integration", "evidence_set_changed", time.Second, func(s *obsDeploymentSnapshot, _ time.Time) { s.notifications.Integrations[0].Integration = "email" }},
		{"source clock regression", "evidence_clock_regressed", time.Second, func(s *obsDeploymentSnapshot, start time.Time) {
			old := start.Add(-time.Second)
			signal := s.notifications.Signals["prometheus_queue_length"]
			signal.ObservedAt = &old
			s.notifications.Signals["prometheus_queue_length"] = signal
		}},
		{"local clock regression", "observation_clock_regressed", -time.Second, func(*obsDeploymentSnapshot, time.Time) {}},
		{"unobserved interval", "observation_gap", api.ObsHealthFreshnessMaxAge + time.Second, func(*obsDeploymentSnapshot, time.Time) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now().UTC().Truncate(time.Second)
			after := start.Add(-90 * time.Second)
			r := newObsDeploymentStability(2 * time.Second)
			serving, notifications := stabilityTestProjection(t, deploymentSnapshotTest(start), after, start)
			r.observe("passed", serving, notifications, start)
			previous := r.snapshot()
			at := start.Add(tc.offset)
			s := deploymentSnapshotTest(at)
			tc.mutate(&s, start)
			serving, notifications = stabilityTestProjection(t, s, after, at)
			if r.observe("passed", serving, notifications, at) || r.status.Resets != 1 || r.status.LastResetReason != tc.reason || r.status.ObservedSeconds != 0 || !r.status.WindowStartedAt.Equal(at) {
				t.Fatalf("did not restart after %s: %+v", tc.name, r.status)
			}
			at = at.Add(2 * time.Second)
			s = deploymentSnapshotTest(at)
			if tc.reason == "evidence_set_changed" {
				tc.mutate(&s, start)
			}
			serving, notifications = stabilityTestProjection(t, s, after, at)
			if !r.observe("passed", serving, notifications, at) || r.status.ObservedSeconds != 2 || previous.Resets != 0 || !previous.WindowStartedAt.Equal(start) {
				t.Fatalf("window failed to recover or mutated its previous snapshot: %+v", r.status)
			}
		})
	}
}

func TestObsDeploymentStabilityRequiresElapsedTime(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	after := start.Add(-90 * time.Second)
	r := newObsDeploymentStability(2 * time.Second)
	serving, notifications := stabilityTestProjection(t, deploymentSnapshotTest(start), after, start)
	r.observe("passed", serving, notifications, start)
	// Source clocks may lead local time within the existing skew tolerance.
	// That must not shorten the locally observed window.
	serving, notifications = stabilityTestProjection(t, deploymentSnapshotTest(start.Add(2*time.Second)), after, start.Add(time.Second))
	if r.observe("passed", serving, notifications, start.Add(time.Second)) || r.status.ObservedSeconds != 1 || r.status.SourceSpanSeconds != 2 || r.status.Reason != "awaiting_observation_window" {
		t.Fatalf("fast source clocks shortened the observation window: %+v", r.status)
	}
	if !r.observe("passed", serving, notifications, start.Add(2*time.Second)) {
		t.Fatal("complete observation/source window failed to qualify")
	}
	if r.observe("passed", nil, notifications, start.Add(3*time.Second)) || r.status.Resets != 1 || r.status.LastResetReason != "evidence_unavailable" || r.status.WindowStartedAt != nil {
		t.Fatal("unavailable source vector preserved a passing window")
	}
}

func TestObsDeploymentStabilityEvidenceBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := deploymentSnapshotTest(now)
	serving, notifications := stabilityTestProjection(t, s, now.Add(-90*time.Second), now)
	for _, mutate := range []func(*api.ObsServingHealth, *obsDeploymentNotifications){
		func(s *api.ObsServingHealth, _ *obsDeploymentNotifications) { s.Probes = nil },
		func(s *api.ObsServingHealth, _ *obsDeploymentNotifications) { s.Probes = append(s.Probes, s.Probes[0]) },
		func(s *api.ObsServingHealth, _ *obsDeploymentNotifications) {
			s.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax+1)
		},
		func(s *api.ObsServingHealth, _ *obsDeploymentNotifications) { s.Inventory.ObservedAt = nil },
		func(s *api.ObsServingHealth, _ *obsDeploymentNotifications) { s.Inventory.ObservedAt = &time.Time{} },
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) { n.Integrations = nil },
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) {
			n.Integrations = append(n.Integrations, n.Integrations[0])
		},
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) {
			n.Integrations = make([]api.ObsNotificationIntegration, api.ObsNotificationIntegrationsMax+1)
		},
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) { n.ReceiverHealth = nil },
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) { n.ReceiverHealth.Groups = nil },
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) {
			n.ReceiverHealth.Groups = append(n.ReceiverHealth.Groups, n.ReceiverHealth.Groups[0])
		},
		func(_ *api.ObsServingHealth, n *obsDeploymentNotifications) {
			n.ReceiverHealth.Groups = make([]api.ObsNotificationReceiver, api.ObsNotificationIntegrationsMax+1)
		},
	} {
		s = deploymentSnapshotTest(now)
		serving, notifications = stabilityTestProjection(t, s, now.Add(-90*time.Second), now)
		mutate(serving, notifications)
		if _, valid := collectObsDeploymentStabilityEvidence(serving, notifications); valid {
			t.Fatal("unbounded, absent or duplicate sources accepted")
		}
	}
	if _, valid := collectObsDeploymentStabilityEvidence(nil, notifications); valid {
		t.Fatal("missing serving accepted")
	}
	if _, valid := collectObsDeploymentStabilityEvidence(serving, nil); valid {
		t.Fatal("missing notifications accepted")
	}
	// Include every pair, including those outside the diagnostic/advisory previews.
	r := newObsDeploymentStability(2 * time.Second)
	for poll := range 2 {
		at := now.Add(time.Duration(poll) * 2 * time.Second)
		s = deploymentSnapshotTest(at)
		serving, notifications = stabilityTestProjection(t, s, now.Add(-90*time.Second), at)
		probe := serving.Probes[0]
		serving.Probes = make([]api.ObsServingProbe, api.ObsServingProbePairsMax)
		for i := range serving.Probes {
			serving.Probes[i] = probe
			serving.Probes[i].URLID = fmt.Sprintf("%064x", i)
		}
		old := now.Add(-time.Minute)
		serving.Probes[len(serving.Probes)-1].LastRunAt = &old
		if r.observe("passed", serving, notifications, at) || r.status.SourceCount != 2016 || r.status.ObservedSeconds != 0 {
			t.Fatalf("a frozen last pair disappeared outside the preview: %+v", r.status)
		}
	}
}
