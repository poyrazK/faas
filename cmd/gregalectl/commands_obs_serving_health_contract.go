package main

import (
	"math"
	"reflect"
	"regexp"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func validObsServingHealth(out api.ObsServingHealth) bool {
	if out.GeneratedAt.IsZero() || out.Scope != operatorobs.ServingHealthScope || out.MaxAgeSeconds != int(api.ObsHealthFreshnessMaxAge.Seconds()) || out.Probes == nil || len(out.Probes) > api.ObsServingProbePairsMax || !reflect.DeepEqual(out.Limitations, operatorobs.ServingHealthLimitations()) || !slices.Contains(operatorobs.ServingHealthReasons(), out.Reason) {
		return false
	}
	inv := out.Inventory
	if !slices.Contains(operatorobs.ServingHealthReasons(), inv.Reason) || inv.ObservedAt != nil && (inv.ObservedAt.IsZero() || inv.ObservedAt.After(out.GeneratedAt.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	if inv.State != "healthy" {
		return slices.Contains([]string{"unavailable", "stale"}, inv.State) && inv.Reason != "" && inv.Configured == nil && inv.ExpectedPairs == nil && out.State == "partial" && out.Reason == inv.Reason && out.Counts == nil && len(out.Probes) == 0 && (inv.State != "stale" || inv.ObservedAt != nil)
	}
	if inv.Reason != "" || inv.Configured == nil || inv.ExpectedPairs == nil || *inv.ExpectedPairs < 0 || *inv.ExpectedPairs > api.ObsServingProbePairsMax || inv.ObservedAt == nil || inv.ObservedAt.Before(out.GeneratedAt.Add(-api.ObsHealthFreshnessMaxAge)) {
		return false
	}
	if !*inv.Configured {
		return *inv.ExpectedPairs == 0 && out.Counts == nil && len(out.Probes) == 0 && out.State == "partial" && out.Reason == "inventory_not_configured"
	}
	if out.Counts == nil || *inv.ExpectedPairs != len(out.Probes) {
		return false
	}
	previous := ""
	for _, probe := range out.Probes {
		key := probe.Node + "/" + probe.URLID
		if key <= previous || !validServingProbe(probe, out.GeneratedAt) {
			return false
		}
		previous = key
	}
	counts := operatorobs.ServingProbeCounts(out.Probes)
	state, reason := operatorobs.ServingProbeOverall(counts)
	return reflect.DeepEqual(*out.Counts, counts) && out.State == state && out.Reason == reason
}

func validServingProbe(probe api.ObsServingProbe, now time.Time) bool {
	if !operatorobs.ServingProbeNodeValid(probe.Node) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(probe.URLID) || !slices.Contains(operatorobs.ServingProbeStates(), probe.State) || !slices.Contains(operatorobs.ServingHealthReasons(), probe.Reason) {
		return false
	}
	for _, value := range []float64{probe.ExpectedFirstByteSeconds, probe.MaxResultAgeSeconds} {
		if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	for _, at := range []*time.Time{probe.EvaluatedAt, probe.LastRunAt} {
		if at != nil && (at.IsZero() || at.After(now.Add(api.ObsHealthClockSkewTolerance))) {
			return false
		}
	}
	if probe.State == "unknown" {
		return probe.Reason != "" && probe.Reporting == nil
	}
	if probe.EvaluatedAt == nil || probe.EvaluatedAt.Before(now.Add(-api.ObsHealthFreshnessMaxAge)) || probe.Reporting == nil || *probe.Reporting != (probe.State != "missing") {
		return false
	}
	if probe.State == "healthy" {
		return probe.Reason == "" && probe.LastRunAt != nil && float64(now.UnixMilli()-probe.LastRunAt.UnixMilli())/1000 <= probe.MaxResultAgeSeconds
	}
	return probe.Reason != ""
}
