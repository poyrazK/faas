package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Both schedulers execute the same complete HTTP journey, with private captures
// and an identity derived from its iteration or scheduled arrival number.
type testLoadJourney struct {
	Shared           *testLoadCollector
	Indexes          []int
	Client           *http.Client
	BaseURL          string
	CorrelationRunID string
	RunID            string
	ConsumerKeys     map[string]string
	Steps            []testHTTPRequest
	Data             map[string]any
	InitialCaptures  map[string]string
}

func (j testLoadJourney) run(ctx context.Context, collector *testLoadCollector, cfg *testLoadConfig, iteration int) {
	if j.Shared != nil {
		j.Shared.mu.Lock()
		j.Shared.started++
		j.Shared.active++
		if j.Shared.active > j.Shared.peak {
			j.Shared.peak = j.Shared.active
		}
		j.Shared.mu.Unlock()
	}
	values := testHTTPValues(fmt.Sprintf("%s-%d", j.RunID, iteration), j.InitialCaptures, j.Data)
	captures := make(map[string]string, len(j.InitialCaptures))
	for name, value := range j.InitialCaptures {
		captures[name] = value
	}
	failed, interrupted := false, false
	for i, step := range j.Steps {
		requestCollector := collector
		if j.Shared != nil {
			requestCollector = j.Shared
		}
		if !requestCollector.reserve(ctx, cfg.RequestLimit) {
			interrupted = true
			break
		}
		if cfg.CorrelationGate != nil {
			cfg.CorrelationGate.RLock()
		}
		result := testHTTPRequestEvidence{Name: step.Name, Method: step.Method}
		requestStarted := time.Now()
		atPhase := requestCollector.phaseIndex(requestStarted)
		correlationRunID := j.CorrelationRunID
		if correlationRunID == "" {
			correlationRunID = j.RunID
		}
		result.CorrelationID = requestCollector.correlationID(cfg, step.Name, correlationRunID, atPhase)
		requestCtx, cancelRequest, deadlineStep := prepareTestRequestDeadline(ctx, cfg, atPhase, step, requestStarted)
		err := runOneTestHTTPRequestWithBody(requestCtx, j.Client, j.BaseURL, deadlineStep, values, j.ConsumerKeys, captures, j.Data, &result, true)
		cancelRequest()
		result.Passed = err == nil
		if err != nil {
			result.Error = err.Error()
		}
		index := i
		if j.Shared != nil {
			index = j.Indexes[i]
		}
		requestCollector.recordAt(index, result, time.Since(requestStarted), requestStarted)
		if cfg.CorrelationGate != nil {
			cfg.CorrelationGate.RUnlock()
		}
		if err != nil {
			failed, interrupted = true, ctx.Err() != nil
			break
		}
	}
	collector.finishIteration(failed, interrupted)
	if j.Shared != nil {
		j.Shared.finishIteration(failed, interrupted)
	}
}

func runTestConcurrencyLoad(ctx context.Context, collector *testLoadCollector, cfg *testLoadConfig, started time.Time, runJourney func(int)) {
	var workers sync.WaitGroup
	for vu := 0; vu < cfg.maxVUs(); vu++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			readyAt := time.Time{}
			for {
				iteration, delay, ok := collector.next(ctx, cfg, started, vu, readyAt)
				if !ok {
					return
				}
				if delay > 0 {
					if !waitTestLoad(ctx, delay) {
						return
					}
					continue
				}
				runJourney(iteration)
				readyAt = time.Now().Add(cfg.Pacing)
			}
		}()
	}
	workers.Wait()
}
