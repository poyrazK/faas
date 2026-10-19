package main

import (
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsInputHealth(out api.ObsInputHealth, role string) bool {
	if out.GeneratedAt.IsZero() || out.Scope != "selected_critical_telemetry_inputs" || out.ExpectedRole != role || out.MaxAgeSeconds != int(api.ObsInputHealthMaxAge.Seconds()) || out.LookbackSeconds != int(api.ObsInputHealthLookback.Seconds()) || out.Jobs == nil || !reflect.DeepEqual(out.AvailableRoles, operatorobs.InputHealthRoles()) || !reflect.DeepEqual(out.Limitations, operatorobs.InputHealthLimitations()) {
		return false
	}
	if role == "" {
		return out.State == "unknown" && out.Reason == "expected_role_not_selected" && len(out.Jobs) == 0
	}
	jobs := []string{"apid", "gatewayd", "schedd", "vmmd"}
	if role == "control-plane-v1" {
		jobs = []string{"apid", "gatewayd", "schedd", "gatewayd-internal", "schedd-compute", "vmmd"}
	} else if role != "single-box-v1" {
		return false
	}
	if out.Reason != "" || len(out.Jobs) != len(jobs) {
		return false
	}
	states := []string{}
	for i, j := range out.Jobs {
		metric := map[string]string{"apid": "apid_daemon_ready", "gatewayd": "gatewayd_public_daemon_ready", "schedd": "schedd_daemon_ready", "gatewayd-internal": "gatewayd_daemon_ready", "schedd-compute": "schedd_daemon_ready", "vmmd": "vmmd_daemon_ready"}[j.Job]
		dynamic := role == "control-plane-v1" && i >= 3
		if j.Job != jobs[i] || len(j.Metrics) != 1 || j.Metrics[0].Metric != metric || !validInputExpectation(j.Expectation, dynamic, out.GeneratedAt) || !validInputTargets(j.Targets, j.Expectation) || !validInputMetric(j.Metrics[0], j.Targets, j.Expectation) || j.State != cliInputState(j.Expectation.State, j.Targets.State, j.Metrics[0].State) {
			return false
		}
		states = append(states, j.State)
	}
	return out.State == cliInputState(states...)
}

func validInputCount(n int) bool { return n >= 0 && n <= api.ObsInputHealthTargetsMax }

func validInputExpectation(e api.ObsInputExpectation, dynamic bool, now time.Time) bool {
	if !dynamic {
		return e.Source == "explicit_minimum" && e.State == "healthy" && e.Reason == "" && e.MinimumTargets != nil && *e.MinimumTargets == 1 && e.InvalidTargets == nil && e.ObservedAt == nil
	}
	if e.Source != "compute_discovery" {
		return false
	}
	if e.State == "unknown" {
		return e.MinimumTargets == nil && e.InvalidTargets == nil && e.ObservedAt == nil && slices.Contains([]string{"discovery_unavailable", "discovery_missing", "discovery_invalid", "discovery_stale"}, e.Reason)
	}
	if e.MinimumTargets == nil || e.InvalidTargets == nil || !validInputCount(*e.MinimumTargets) || !validInputCount(*e.InvalidTargets) || !validInputCount(*e.MinimumTargets+*e.InvalidTargets) || e.ObservedAt == nil || e.ObservedAt.Before(now.Add(-api.ObsInputHealthMaxAge)) || e.ObservedAt.After(now.Add(api.ObsHealthClockSkewTolerance)) {
		return false
	}
	if *e.InvalidTargets > 0 {
		return e.State == "degraded" && e.Reason == "discovery_invalid_targets"
	}
	return e.State == "healthy" && e.Reason == ""
}

func validInputTargets(t api.ObsInputTargets, e api.ObsInputExpectation) bool {
	c := t.Counts
	if c == nil {
		return t.State == "unknown" && slices.Contains([]string{"source_unavailable", "invalid_evidence"}, t.Reason)
	}
	if !validInputCount(c.Observed) || !validInputCount(c.FreshUp) || !validInputCount(c.Down) || !validInputCount(c.Stale) || c.FreshUp+c.Down+c.Stale != c.Observed {
		return false
	}
	if e.MinimumTargets == nil {
		if c.MissingMinimum != nil || c.Extra != nil {
			return false
		}
	} else {
		if c.MissingMinimum == nil || *c.MissingMinimum != max(0, *e.MinimumTargets-c.Observed) {
			return false
		}
		if e.Source == "compute_discovery" {
			if c.Extra == nil || *c.Extra != max(0, c.Observed-*e.MinimumTargets) {
				return false
			}
		} else if c.Extra != nil {
			return false
		}
	}
	if c.Down > 0 || c.Stale > 0 || (c.MissingMinimum != nil && *c.MissingMinimum > 0) {
		return t.State == "degraded" && t.Reason == "scrape_coverage_gap"
	}
	if c.Extra != nil && *c.Extra > 0 {
		return t.State == "partial" && t.Reason == "inventory_count_mismatch"
	}
	if e.MinimumTargets == nil {
		return t.State == "unknown" && t.Reason == "expectation_unavailable"
	}
	return t.State == "healthy" && t.Reason == ""
}

func validInputMetric(m api.ObsInputMetric, t api.ObsInputTargets, e api.ObsInputExpectation) bool {
	if t.Counts == nil {
		return m.State == "unknown" && m.Reason == "target_evidence_unavailable" && m.Counts == nil
	}
	if t.Counts.FreshUp == 0 {
		if t.Counts.Observed == 0 && e.State == "healthy" && e.MinimumTargets != nil && *e.MinimumTargets == 0 {
			return m.State == "not_applicable" && m.Reason == "zero_expected_targets" && m.Counts != nil && *m.Counts == (api.ObsInputMetricCounts{})
		}
		return m.State == "unknown" && m.Reason == "no_fresh_up_targets" && m.Counts == nil
	}
	c := m.Counts
	if c == nil {
		return m.State == "unknown" && slices.Contains([]string{"source_unavailable", "invalid_evidence"}, m.Reason)
	}
	if !validInputCount(c.Fresh) || !validInputCount(c.Missing) || !validInputCount(c.Stale) || c.Expected != t.Counts.FreshUp || c.Fresh+c.Missing+c.Stale != c.Expected {
		return false
	}
	if c.Missing > 0 || c.Stale > 0 {
		return m.State == "degraded" && m.Reason == "metric_sample_gap"
	}
	return m.State == "healthy" && m.Reason == ""
}

func cliInputState(states ...string) string {
	unknown, known := false, false
	for _, s := range states {
		switch s {
		case "degraded":
			return "degraded"
		case "partial":
			unknown, known = true, true
		case "unknown":
			unknown = true
		case "healthy", "not_applicable":
			known = true
		default:
			return "invalid"
		}
	}
	if unknown && known {
		return "partial"
	}
	if unknown || !known {
		return "unknown"
	}
	return "healthy"
}
