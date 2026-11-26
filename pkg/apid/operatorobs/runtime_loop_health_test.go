package operatorobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func loopQueries(spec runtimeBuildJobSpec, loop string) (string, string) {
	prefix := strings.TrimSuffix(spec.metric, "_daemon_build_info")
	return fmt.Sprintf(`%s_loop_stalled{job=%q,loop=%q}`, prefix, spec.job, loop), fmt.Sprintf(`%s_loop_last_beat_age_seconds{job=%q,loop=%q}`, prefix, spec.job, loop)
}

func addLoopFixture(f inputFixture, role string, now time.Time) {
	addRuntimeAppTaskOperationFixture(f, now)
	addRuntimeAppTaskResultFixture(f, now)
	addRuntimeCommandCronOutcomeFixture(f, now)
	addRuntimeCommandCronTimingFixture(f, now)
	addRuntimeHTTPDeliveryFixture(f, now)
	addRuntimeDelayedDispatchFixture(f, now)
	addRuntimeCommandCronFixture(f, now)
	addRuntimeAppTaskFixture(f, now)
	for _, spec := range runtimeBuildJobSpecs(role) {
		for _, loop := range runtimeLoopsForDaemon(spec.daemon) {
			stalled, age := loopQueries(spec, loop)
			for _, q := range []string{stalled, "timestamp(" + stalled + ")", age, "timestamp(" + age + ")"} {
				f[q] = []inputFixtureRow{}
				for _, up := range f[fmt.Sprintf(`up{job=%q}`, spec.job)] {
					value := float64(0)
					if strings.Contains(q, "age_seconds") {
						value = 1
					}
					if strings.HasPrefix(q, "timestamp(") {
						value = float64(now.Unix())
					}
					row := inputRow(spec.job, up.Metric["instance"], "", value)
					for k, v := range up.Metric {
						row.Metric[k] = v
					}
					row.Metric["loop"] = loop
					f[q] = append(f[q], row)
				}
			}
		}
	}
}

func TestLoopHealthReportedStallsAndEvidenceGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, state, itemState string
		reported               bool
		change                 func(inputFixture, string, string)
	}{
		{"no_reported_stall", "no_reported_stall", "no_reported_stall", false, func(inputFixture, string, string) {}},
		{"ready_but_stalled", "stalled", "stalled", true, func(f inputFixture, s, a string) { f[s][0].Value[1] = "1"; f[a][0].Value[1] = "240" }},
		{"missing_age_keeps_report", "stalled", "missing", true, func(f inputFixture, s, a string) { f[s][0].Value[1] = "1"; f[a] = nil; f["timestamp("+a+")"] = nil }},
		{"missing_stall", "incomplete", "missing", false, func(f inputFixture, s, a string) { f[s] = nil; f["timestamp("+s+")"] = nil }},
		{"stale_stall", "incomplete", "stale", false, func(f inputFixture, s, a string) {
			f[s][0].Value[1] = "1"
			f["timestamp("+s+")"][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"stale_age_keeps_report", "stalled", "stale", true, func(f inputFixture, s, a string) {
			f[s][0].Value[1] = "1"
			f["timestamp("+a+")"][0].Value[1] = fmt.Sprint(now.Add(-3 * time.Minute).Unix())
		}},
		{"before_boundary", "incomplete", "unavailable", false, func(f inputFixture, s, a string) {
			f["timestamp("+s+")"][0].Value[1] = fmt.Sprint(now.Add(-100 * time.Second).Unix())
		}},
		{"future_clock", "incomplete", "unavailable", false, func(f inputFixture, s, a string) {
			f["timestamp("+s+")"][0].Value[1] = fmt.Sprint(now.Add(time.Hour).Unix())
		}},
		{"invalid_stall_value", "incomplete", "unavailable", false, func(f inputFixture, s, a string) { f[s][0].Value[1] = "2" }},
		{"negative_age_keeps_report", "stalled", "unavailable", true, func(f inputFixture, s, a string) { f[s][0].Value[1] = "1"; f[a][0].Value[1] = "-1" }},
		{"conflicting_stall", "incomplete", "conflicting", false, func(f inputFixture, s, a string) {
			row := f[s][0]
			row.Value = [2]any{row.Value[0], "1"}
			f[s] = append(f[s], row)
		}},
		{"unknown_loop", "incomplete", "unavailable", false, func(f inputFixture, s, a string) { f[s][0].Metric["loop"] = "private-provider-error" }},
		{"orphan_clock", "incomplete", "unavailable", false, func(f inputFixture, s, a string) { f["timestamp("+s+")"][0].Metric["extra"] = "private-provider-error" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, selected, registry := runtimeFixture("single-box-v2", now, 0)
			addLoopFixture(f, selected.role, now)
			stalled, age := loopQueries(runtimeBuildJobSpecs(selected.role)[2], "main")
			tc.change(f, stalled, age)
			out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
			h := out.LoopHealth
			if !RuntimeBuildsValid(out.ObsRuntimeBuilds) || !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, h) || out.State != "matching" || out.Readiness.State != "ready" || out.Process.State != "baseline" || h.State != tc.state {
				t.Fatalf("bad loop assessment: %+v", h)
			}
			found := false
			for _, item := range h.Items {
				if item.Job == "schedd" && item.Loop == "main" {
					found = true
					reported := item.ReportedStalled != nil && *item.ReportedStalled
					if item.State != tc.itemState || reported != tc.reported {
						t.Fatalf("lost partial evidence: %+v", item)
					}
				}
			}
			if !found {
				t.Fatal("expected main loop absent")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private-provider-error") || strings.Contains(string(body), "private-host") {
				t.Fatal("raw loop labels leaked")
			}
		})
	}
}

func TestLoopHealthAllProfilesAndStallBeyondPreview(t *testing.T) {
	lengthenRuntimeCollectionBudget(t)
	now := time.Now().UTC().Truncate(time.Second)
	expected := map[string]int{"single-box-v1": 6, "single-box-v2": 8, "control-plane-v1": 14, "control-plane-v2": 18}
	for _, role := range RuntimeBuildRoles() {
		f, selected, registry := runtimeFixture(role, now, 2)
		addLoopFixture(f, role, now)
		out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
		if !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, out.LoopHealth) || out.LoopHealth.Counts.Expected != expected[role] || out.LoopHealth.Counts.NoReportedStall != expected[role] {
			t.Fatalf("wrong loop profile %s: %+v", role, out.LoopHealth)
		}
	}
	f, selected, registry := runtimeFixture("control-plane-v2", now, 50)
	addLoopFixture(f, selected.role, now)
	stalled, _ := loopQueries(runtimeBuildJobSpecs(selected.role)[7], "runtime")
	f[stalled][49].Value[1] = "1"
	out := RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), registry, selected, now)
	h := out.LoopHealth
	// Full-set counts and the unhealthy-first preview survive the shared byte
	// cap, which can retain fewer than the maximum 32 target details.
	if !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, h) || h.Counts.Expected != 354 || h.Counts.ReportedStalls != 1 || h.Counts.NoReportedStall != 353 || h.OmittedItems+len(h.Items) != 354 || len(h.Items) == 0 || len(h.Items) > api.ObsRuntimeBuildPreviewMax || h.Items[0].Job != "imaged" || h.Items[0].Node != "node-0049" || h.State != "stalled" {
		t.Fatalf("preview hid a reported stall: %+v", h)
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	if len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("unbounded API envelope")
	}
	// Worst-case identity labels and loop details still fit one shared response.
	for i := range out.Items {
		out.Items[i].Node = strings.Repeat("a", api.ObsServingProbeNodeMaxBytes)
		out.Items[i].Version = strings.Repeat("v", api.ObsRuntimeBuildVersionMaxBytes)
	}
	for i := range h.Items {
		h.Items[i].Node = strings.Repeat("a", api.ObsServingProbeNodeMaxBytes)
	}
	out = fitRuntimeLoopResponse(out)
	body, _ = json.MarshalIndent(out, "", "  ")
	if !RuntimeBuildsValid(out.ObsRuntimeBuilds) || !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, h) || len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatal("fitting lost counts or envelope")
	}
}

func TestLoopHealthPartialSourceAndUnknownInventory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, selected, registry := runtimeFixture("single-box-v1", now, 0)
	addLoopFixture(f, selected.role, now)
	stalled, age := loopQueries(runtimeBuildJobSpecs(selected.role)[2], "main")
	f[stalled][0].Value[1] = "1"
	out := RuntimeBuildsWithLoopHealth(t.Context(), runtimeProcessClient(t, f, nil, now, time.Time{}, age), registry, selected, now)
	if !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, out.LoopHealth) || out.LoopHealth.State != "stalled" || !out.LoopHealth.Incomplete || out.LoopHealth.Counts.ReportedStalls != 1 || out.Readiness.State != "ready" {
		t.Fatalf("source gap hid a report: %+v", out.LoopHealth)
	}
	noSource := RuntimeBuildsWithLoopHealth(t.Context(), nil, registry, selected, now)
	if !RuntimeLoopHealthValid(noSource.ObsRuntimeBuilds, noSource.LoopHealth) || noSource.LoopHealth.Counts.Unavailable != 6 || noSource.LoopHealth.State != "incomplete" {
		t.Fatal("unavailable source became clear")
	}
	f, selected, _ = runtimeFixture("control-plane-v2", now, 0)
	addLoopFixture(f, selected.role, now)
	out = RuntimeBuildsWithLoopHealth(t.Context(), inputFixtureClient(t, f, now), nil, selected, now)
	if !RuntimeLoopHealthValid(out.ObsRuntimeBuilds, out.LoopHealth) || !out.LoopHealth.Incomplete || out.LoopHealth.State != "incomplete" {
		t.Fatal("unknown inventory became an empty healthy fleet")
	}
}
