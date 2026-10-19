// adr: 650 — sampled gateway phase timings, with interrupted work kept separate.
package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

var dataAPITimingPhases = []string{"verification", "refresh", "readiness", "signing", "upstream", "streaming"}

type dataAPITimingRecord struct {
	DurationMS *float64 `json:"duration_ms"`
	Complete   *bool    `json:"complete"`
}

type dataAPIPhaseTimingStats struct {
	Observed             int            `json:"observed"`
	Completed            int            `json:"completed"`
	Interrupted          int            `json:"interrupted"`
	LatencyMS            dataAPILatency `json:"latency_ms"`
	InterruptedElapsedMS dataAPILatency `json:"interrupted_elapsed_ms"`
	completedSamples     []float64
	interruptedSamples   []float64
}

type dataAPITimingStats struct {
	ObservedRequests int                                 `json:"observed_requests"`
	WithoutTimings   int                                 `json:"without_timings"`
	ByPhase          map[string]*dataAPIPhaseTimingStats `json:"by_phase"`
}

func validateDataAPITimings(timings map[string]dataAPITimingRecord) error {
	if timings == nil {
		return nil // Older runtimes and requests which never entered admission.
	}
	if len(timings) == 0 {
		return errors.New("empty request timings")
	}
	for phase, timing := range timings {
		if !dataAPIDiagnosticAllowed(phase, dataAPITimingPhases...) || timing.DurationMS == nil || timing.Complete == nil || math.IsNaN(*timing.DurationMS) || math.IsInf(*timing.DurationMS, 0) || *timing.DurationMS < 0 {
			return errors.New("invalid request timing metadata")
		}
	}
	return nil
}

func (s *dataAPITimingStats) add(timings map[string]dataAPITimingRecord) {
	if timings == nil {
		s.WithoutTimings++
		return
	}
	s.ObservedRequests++
	if s.ByPhase == nil {
		s.ByPhase = map[string]*dataAPIPhaseTimingStats{}
	}
	for phase, timing := range timings {
		stats := s.ByPhase[phase]
		if stats == nil {
			stats = &dataAPIPhaseTimingStats{}
			s.ByPhase[phase] = stats
		}
		stats.Observed++
		if *timing.Complete {
			stats.Completed++
			stats.completedSamples = append(stats.completedSamples, *timing.DurationMS)
		} else {
			stats.Interrupted++
			stats.interruptedSamples = append(stats.interruptedSamples, *timing.DurationMS)
		}
	}
}

func dataAPIPercentiles(samples []float64) dataAPILatency {
	if len(samples) == 0 {
		return dataAPILatency{}
	}
	sort.Float64s(samples)
	rank := func(p float64) *float64 {
		value := samples[int(math.Ceil(p*float64(len(samples))))-1]
		return &value
	}
	return dataAPILatency{P50: rank(.50), P95: rank(.95), P99: rank(.99)}
}

func (s *dataAPITimingStats) finish() {
	if s.ByPhase == nil {
		s.ByPhase = map[string]*dataAPIPhaseTimingStats{}
	}
	for _, stats := range s.ByPhase {
		stats.LatencyMS = dataAPIPercentiles(stats.completedSamples)
		stats.InterruptedElapsedMS = dataAPIPercentiles(stats.interruptedSamples)
		stats.completedSamples, stats.interruptedSamples = nil, nil
	}
}

func writeDataAPITimings(stats dataAPITimingStats) error {
	if _, err := fmt.Fprintf(osStdout, "Phase timing coverage: %d observed, %d without timings\n", stats.ObservedRequests, stats.WithoutTimings); err != nil {
		return err
	}
	for _, phase := range dataAPITimingPhases {
		s := stats.ByPhase[phase]
		if s == nil {
			continue
		}
		latency := "n/a"
		if s.LatencyMS.P50 != nil && s.LatencyMS.P95 != nil && s.LatencyMS.P99 != nil {
			latency = fmt.Sprintf("%.3f / %.3f / %.3f ms", *s.LatencyMS.P50, *s.LatencyMS.P95, *s.LatencyMS.P99)
		}
		if _, err := fmt.Fprintf(osStdout, "  %s: %d completed, %d interrupted; completed p50/p95/p99: %s\n", phase, s.Completed, s.Interrupted, latency); err != nil {
			return err
		}
	}
	return nil
}

func rankDataAPIQueryShapes(shapes []dataAPIShapeStats, order string) {
	sort.Slice(shapes, func(i, j int) bool {
		a, b := shapes[i], shapes[j]
		if order == "p95" {
			x, y := a.Stats.LatencyMS.P95, b.Stats.LatencyMS.P95
			if (x == nil) != (y == nil) {
				return x != nil
			}
			if x != nil && *x != *y {
				return *x > *y
			}
		}
		if order == "errors" && a.Stats.Errors != b.Stats.Errors {
			return a.Stats.Errors > b.Stats.Errors
		}
		if a.Stats.Requests != b.Stats.Requests {
			return a.Stats.Requests > b.Stats.Requests
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.Fingerprint != b.Fingerprint {
			return a.Fingerprint < b.Fingerprint
		}
		return a.ShapeID < b.ShapeID
	})
}
