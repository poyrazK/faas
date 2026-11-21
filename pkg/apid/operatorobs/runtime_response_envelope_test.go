package operatorobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRuntimeResponseEnvelopePreservesFittingResponse(t *testing.T) {
	for _, items := range [][]api.ObsRuntimeBuildItem{nil, {}, {{Job: "apid", State: "matching"}}} {
		in := RuntimeBuildsLoopResponse{ObsRuntimeBuilds: api.ObsRuntimeBuilds{Items: items, OmittedItems: 9}}
		before, err := json.MarshalIndent(in, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		after := runtimeEnvelopeJSON(t, fitRuntimeLoopResponse(in))
		if !bytes.Equal(before, after) {
			t.Fatalf("fitting response changed: %s", after)
		}
	}
}

func TestRuntimeResponseEnvelopePreservesPriorityAndFullCoverage(t *testing.T) {
	in := runtimeEnvelopeFixture()
	identity := slices.Clone(in.Items)
	loops := slices.Clone(in.LoopHealth.Items)
	cron := slices.Clone(in.CommandCronOutcomes.Items)
	cronSummary := in.CommandCronOutcomes.RuntimeCommandCronOutcomesSummary
	dispatchSummary := in.DelayedTaskDispatch.RuntimeDelayedTaskDispatchSummary
	httpSummary := in.HTTPCronDelivery.RuntimeHTTPCronDeliverySummary
	counts := in.Counts
	out := fitRuntimeLoopResponse(in)
	if !reflect.DeepEqual(out.Items, identity) || !reflect.DeepEqual(out.LoopHealth.Items, loops) || out.OmittedItems != 9 {
		t.Fatal("advisory previews displaced identity or loop evidence")
	}
	if len(out.DelayedTaskDispatch.Items) != 0 || out.DelayedTaskDispatch.OmittedItems != 41 ||
		len(out.HTTPCronDelivery.Items) != 0 || out.HTTPCronDelivery.OmittedItems != 32 {
		t.Fatal("lower-priority previews were not exhausted first")
	}
	retained := len(out.CommandCronOutcomes.Items)
	if retained == 0 || retained >= len(cron) || out.CommandCronOutcomes.OmittedItems != 9+len(cron)-retained {
		t.Fatalf("incorrect cron clipping: retained=%d omitted=%d", retained, out.CommandCronOutcomes.OmittedItems)
	}
	if !reflect.DeepEqual(out.CommandCronOutcomes.Items, cron[:retained]) || out.Counts != counts ||
		!reflect.DeepEqual(out.CommandCronOutcomes.RuntimeCommandCronOutcomesSummary, cronSummary) ||
		!reflect.DeepEqual(out.DelayedTaskDispatch.RuntimeDelayedTaskDispatchSummary, dispatchSummary) ||
		!reflect.DeepEqual(out.HTTPCronDelivery.RuntimeHTTPCronDeliverySummary, httpSummary) {
		t.Fatal("clipping changed full-set summaries or retained evidence")
	}
	runtimeEnvelopeJSON(t, out)
	out.CommandCronOutcomes.Items = cron[:retained+1]
	out.CommandCronOutcomes.OmittedItems--
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(body) < api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatalf("a further cron item should exceed the envelope: bytes=%d err=%v", len(body), err)
	}
}

func TestRuntimeResponseEnvelopeClipsIdentityLast(t *testing.T) {
	for _, tc := range []struct {
		name       string
		items      int
		reasonSize int
	}{
		{"large_inventory", 32, 3000},
		{"omission_count_gains_digit", 2, 32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := RuntimeBuildsLoopResponse{ObsRuntimeBuilds: api.ObsRuntimeBuilds{
				Counts: api.ObsRuntimeBuildCounts{Expected: tc.items + 9, Observed: tc.items + 9}, OmittedItems: 9,
			}}
			for i := range tc.items {
				in.Items = append(in.Items, api.ObsRuntimeBuildItem{Job: "apid", TargetID: fmt.Sprint(i), Reason: strings.Repeat("x", tc.reasonSize)})
			}
			out := fitRuntimeLoopResponse(in)
			retained := len(out.Items)
			if retained == 0 || retained >= tc.items || out.OmittedItems != 9+tc.items-retained ||
				out.Counts != in.Counts || !reflect.DeepEqual(out.Items, in.Items[:retained]) {
				t.Fatalf("incorrect identity preview: retained=%d omitted=%d", retained, out.OmittedItems)
			}
			runtimeEnvelopeJSON(t, out)
			out.Items = in.Items[:retained+1]
			out.OmittedItems--
			body, err := json.MarshalIndent(out, "", "  ")
			if err != nil || len(body) < api.ObsRuntimeBuildResponseMaxBytes {
				t.Fatalf("a further identity item should exceed the envelope: bytes=%d err=%v", len(body), err)
			}
		})
	}
}

func runtimeEnvelopeJSON(t *testing.T, out RuntimeBuildsLoopResponse) []byte {
	t.Helper()
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil || len(body) >= api.ObsRuntimeBuildResponseMaxBytes {
		t.Fatalf("response exceeds envelope: bytes=%d err=%v", len(body), err)
	}
	return body
}

func runtimeEnvelopeFixture() RuntimeBuildsLoopResponse {
	point := &RuntimeAppTaskOperationPoint{Value: 10, ObservedAt: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)}
	cron := &RuntimeCommandCronOutcomes{RuntimeCommandCronOutcomesSummary: newRuntimeCommandCronOutcomesSummary(), OmittedItems: 9}
	cron.Counts = RuntimeAppTaskOperationCounts{Expected: 41 * 29, Observed: 41 * 29, Baseline: 41 * 29}
	dispatch := &RuntimeDelayedTaskDispatch{RuntimeDelayedTaskDispatchSummary: newRuntimeDelayedTaskDispatchSummary(), OmittedItems: 9}
	http := &RuntimeHTTPCronDelivery{RuntimeHTTPCronDeliverySummary: newRuntimeHTTPCronDeliverySummary()}
	out := RuntimeBuildsLoopResponse{
		ObsRuntimeBuilds:    api.ObsRuntimeBuilds{Counts: api.ObsRuntimeBuildCounts{Expected: 41, Observed: 41, Matching: 41}, OmittedItems: 9},
		CommandCronOutcomes: cron, DelayedTaskDispatch: dispatch, HTTPCronDelivery: http,
		LoopHealth: &RuntimeLoopHealth{RuntimeLoopSummary: RuntimeLoopSummary{Counts: RuntimeLoopCounts{Expected: 41, Observed: 41}}},
	}
	for i := range 32 {
		target := fmt.Sprintf("target-%02d", i)
		out.Items = append(out.Items, api.ObsRuntimeBuildItem{Job: "apid", TargetID: target, State: "matching", GitSHA: strings.Repeat("a", 40)})
		out.LoopHealth.Items = append(out.LoopHealth.Items, RuntimeLoopItem{Job: "apid", TargetID: target, Loop: "runtime", State: "no_reported_stall"})
		item := RuntimeCommandCronOutcomeItem{Job: "apid", TargetID: target}
		for _, dimension := range runtimeCommandCronOutcomeDimensions() {
			item.Signals = append(item.Signals, RuntimeCommandCronOutcomeSignal{RuntimeCommandCronOutcomeDimension: dimension, State: "baseline", Current: point})
		}
		cron.Items = append(cron.Items, item)
		dispatch.Items = append(dispatch.Items, RuntimeDelayedTaskDispatchItem{Job: "apid", TargetID: target})
		http.Items = append(http.Items, RuntimeHTTPCronDeliveryItem{Job: "apid", TargetID: target})
	}
	return out
}

func BenchmarkRuntimeResponseEnvelope(b *testing.B) {
	in := runtimeEnvelopeFixture()
	b.ReportAllocs()
	for b.Loop() {
		out := in
		cron, dispatch, http := *in.CommandCronOutcomes, *in.DelayedTaskDispatch, *in.HTTPCronDelivery
		out.CommandCronOutcomes, out.DelayedTaskDispatch, out.HTTPCronDelivery = &cron, &dispatch, &http
		fitRuntimeLoopResponse(out)
	}
}
