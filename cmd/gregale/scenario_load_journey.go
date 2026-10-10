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
	Client          *http.Client
	BaseURL         string
	RunID           string
	ConsumerKeys    map[string]string
	Steps           []testHTTPRequest
	Data            map[string]any
	InitialCaptures map[string]string
}

func (j testLoadJourney) run(ctx context.Context, collector *testLoadCollector, cfg *testLoadConfig, iteration int) {
	values := testHTTPValues(fmt.Sprintf("%s-%d", j.RunID, iteration), j.InitialCaptures, j.Data)
	captures := make(map[string]string, len(j.InitialCaptures))
	for name, value := range j.InitialCaptures {
		captures[name] = value
	}
	failed, interrupted := false, false
	for i, step := range j.Steps {
		if !collector.reserve(ctx, cfg.RequestLimit) {
			interrupted = true
			break
		}
		result := testHTTPRequestEvidence{Name: step.Name, Method: step.Method}
		requestStarted := time.Now()
		err := runOneTestHTTPRequestWithBody(ctx, j.Client, j.BaseURL, step, values, j.ConsumerKeys, captures, j.Data, &result, true)
		result.Passed = err == nil
		if err != nil {
			result.Error = err.Error()
		}
		collector.record(i, result, time.Since(requestStarted))
		if err != nil {
			failed, interrupted = true, ctx.Err() != nil
			break
		}
	}
	collector.finishIteration(failed, interrupted)
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
