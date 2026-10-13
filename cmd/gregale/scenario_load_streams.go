package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Streams partition the journey so captures and admission capacity stay private.
type testLoadStreamSpec struct {
	Name     string   `yaml:"name"`
	Requests []string `yaml:"requests"`
	Rate     int      `yaml:"rate"`
	VUs      int      `yaml:"vus"`
}
type testLoadStreamEvidence struct {
	Name     string                  `json:"name"`
	Requests []string                `json:"requests"`
	VUs      int                     `json:"vus"`
	Arrival  testLoadArrivalEvidence `json:"arrival"`
}

func validateTestLoadStreams(spec *testLoadSpec) error {
	if len(spec.Streams) == 0 {
		return nil
	}
	if len(spec.Streams) < 2 || len(spec.Streams) > 20 {
		return errors.New("load.streams needs between 2 and 20 streams")
	}
	if spec.Duration == "" || spec.VUs != 0 || spec.Rate != 0 || spec.Iterations != 0 || spec.Pacing != "" || len(spec.Stages) > 0 {
		return errors.New("load.streams requires duration and cannot use top-level vus, rate, iterations, pacing, or stages")
	}
	names := map[string]bool{}
	requests := map[string]bool{}
	vus, rate := 0, 0
	for _, stream := range spec.Streams {
		if !testHTTPNamePattern.MatchString(stream.Name) || names[stream.Name] {
			return fmt.Errorf("invalid or duplicate load stream name %q", stream.Name)
		}
		names[stream.Name] = true
		if stream.VUs < 1 || stream.VUs > testLoadMaxVUs || stream.Rate < 1 || stream.Rate > testLoadMaxRate || len(stream.Requests) == 0 {
			return fmt.Errorf("stream %q needs requests, vus between 1 and %d, and rate between 1 and %d", stream.Name, testLoadMaxVUs, testLoadMaxRate)
		}
		vus += stream.VUs
		rate += stream.Rate
		for _, name := range stream.Requests {
			if !testHTTPNamePattern.MatchString(name) || requests[name] {
				return fmt.Errorf("invalid or repeated stream request %q", name)
			}
			requests[name] = true
		}
	}
	if vus > testLoadMaxVUs || rate > testLoadMaxRate {
		return errors.New("combined stream capacity exceeds 50 VUs or 1000 journeys/s")
	}
	return nil
}

func validateTestLoadStreamRequests(cfg *testLoadConfig, steps []testHTTPRequest) error {
	if len(cfg.Streams) == 0 {
		return nil
	}
	declared := map[string]int{}
	for _, step := range steps {
		declared[step.Name]++
	}
	captureOwners := map[string]string{}
	for _, step := range steps {
		for capture := range step.Capture {
			captureOwners[capture] = step.Name
			captureOwners["steps."+step.Name+"."+capture] = step.Name
		}
	}
	selected := map[string]bool{}
	for _, stream := range cfg.Streams {
		available := map[string]bool{}
		for _, name := range stream.Requests {
			if declared[name] != 1 {
				return fmt.Errorf("stream %q request %q must name exactly one load request", stream.Name, name)
			}
			selected[name] = true
			for _, step := range steps {
				if step.Name != name {
					continue
				}
				encoded, err := json.Marshal(step)
				if err != nil {
					return fmt.Errorf("stream %q request %q: %w", stream.Name, name, err)
				}
				for _, match := range testSecretReferencePattern.FindAllString(string(encoded), -1) {
					ref := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
					if owner, ok := captureOwners[ref]; ok && !available[owner] {
						return fmt.Errorf("stream %q request %q references capture from %q before it runs in this stream", stream.Name, name, owner)
					}
				}
				available[name] = true
			}
		}
	}
	for name := range declared {
		if !selected[name] {
			return fmt.Errorf("load request %q must belong to a stream", name)
		}
	}
	return nil
}

func runTestLoadStreams(ctx context.Context, shared *testLoadCollector, cfg *testLoadConfig, started time.Time, journey testLoadJourney) []testLoadStreamEvidence {
	evidence := make([]testLoadStreamEvidence, len(cfg.Streams))
	var workers sync.WaitGroup
	for i, stream := range cfg.Streams {
		workers.Add(1)
		go func(i int, stream testLoadStreamSpec) {
			defer workers.Done()
			local := &testLoadCollector{}
			streamCfg := &testLoadConfig{VUs: stream.VUs, Rate: stream.Rate, Duration: cfg.Duration, RequestLimit: cfg.RequestLimit, ChaosSchedule: cfg.ChaosSchedule, ChaosPhaseClock: cfg.ChaosPhaseClock, CorrelationGate: cfg.CorrelationGate}
			j := journey
			j.CorrelationRunID = journey.RunID
			j.RunID = journey.RunID + "-" + stream.Name
			j.Shared = shared
			j.Steps = nil
			j.Indexes = nil
			for _, name := range stream.Requests {
				for index, step := range journey.Steps {
					if step.Name == name {
						j.Steps = append(j.Steps, step)
						j.Indexes = append(j.Indexes, index)
					}
				}
			}
			runTestArrivalLoad(ctx, local, streamCfg, started, func(iteration int) { j.run(ctx, local, streamCfg, iteration) })
			arrival := testLoadArrivalEvidence{TargetRate: stream.Rate, Planned: streamCfg.plannedArrivals(), Scheduled: local.arrivalsScheduled, DroppedCapacity: local.droppedCapacity, DroppedLate: local.droppedLate, Dropped: local.droppedCapacity + local.droppedLate, SchedulingDurationMS: loadMilliseconds(local.schedulingDuration)}
			if local.schedulingDuration > 0 {
				arrival.StartedPerSecond = float64(local.started) / local.schedulingDuration.Seconds()
			}
			evidence[i] = testLoadStreamEvidence{Name: stream.Name, Requests: append([]string(nil), stream.Requests...), VUs: stream.VUs, Arrival: arrival}
		}(i, stream)
	}
	workers.Wait()
	return evidence
}
