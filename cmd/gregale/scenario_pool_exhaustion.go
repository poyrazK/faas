package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type testPoolExhaustionSpec struct {
	Path                string `yaml:"path" json:"path"`
	As                  string `yaml:"as,omitempty" json:"as,omitempty"`
	Capacity            int64  `yaml:"capacity" json:"capacity"`
	MaxQueue            int64  `yaml:"max_queue" json:"max_queue"`
	MaxWait             string `yaml:"max_wait" json:"max_wait"`
	MinQueuedRequests   int64  `yaml:"min_queued_requests" json:"min_queued_requests"`
	MinCancelledWaiters int64  `yaml:"min_cancelled_waiters,omitempty" json:"min_cancelled_waiters,omitempty"`
	Within              string `yaml:"within" json:"within"`
	Sustain             string `yaml:"sustain" json:"sustain"`
	Window              string `yaml:"window" json:"window"`
	MinCompletions      int64  `yaml:"min_completions" json:"min_completions"`
}
type testPoolSnapshot struct {
	Instance   string `json:"instance_id"`
	Capacity   int64  `json:"capacity"`
	Active     int64  `json:"active"`
	Queued     int64  `json:"queued"`
	PeakActive int64  `json:"peak_active"`
	PeakQueued int64  `json:"peak_queued"`
	Created    int64  `json:"waiters_created"`
	Finished   int64  `json:"waiters_finished"`
	Cancelled  int64  `json:"waiters_cancelled"`
	TimedOut   int64  `json:"waiters_timed_out"`
	MaxWaitMS  int64  `json:"max_wait_ms"`
	Completed  int64  `json:"completed"`
}
type testPoolSample struct {
	At       time.Time
	Phase    int
	Snapshot *testPoolSnapshot
	Error    string
}
type testPoolThroughputWindow struct {
	StartMS   int64 `json:"start_ms"`
	EndMS     int64 `json:"end_ms"`
	Completed int64 `json:"completed"`
	Required  int64 `json:"required"`
	Passed    bool  `json:"passed"`
}
type testPoolPhaseEvidence struct {
	Passed         bool                       `json:"passed"`
	Samples        int                        `json:"samples"`
	QueuedRequests int64                      `json:"queued_requests"`
	PeakActive     int64                      `json:"peak_active"`
	PeakQueued     int64                      `json:"peak_queued"`
	MaxWaitMS      int64                      `json:"max_wait_ms"`
	RecoveryMS     *int64                     `json:"recovery_ms,omitempty"`
	Windows        []testPoolThroughputWindow `json:"windows,omitempty"`
	Error          string                     `json:"error,omitempty"`
}
type testPoolDrainEvidence struct {
	Passed           bool              `json:"passed"`
	Snapshot         *testPoolSnapshot `json:"snapshot,omitempty"`
	CancelledWaiters int64             `json:"cancelled_waiters"`
	Error            string            `json:"error,omitempty"`
}
type testPoolExhaustionEvidence struct {
	Source           string                  `json:"source"`
	Budget           *testPoolExhaustionSpec `json:"budget"`
	Baseline         *testPoolSnapshot       `json:"baseline,omitempty"`
	PeakActive       int64                   `json:"peak_active"`
	PeakQueued       int64                   `json:"peak_queued"`
	MaxWaitMS        int64                   `json:"max_wait_ms"`
	FirstFailedCycle int                     `json:"first_failed_cycle,omitempty"`
	Cancelled        bool                    `json:"cancelled"`
	Passed           bool                    `json:"passed"`
	PostDrain        *testPoolDrainEvidence  `json:"post_drain,omitempty"`
	Error            string                  `json:"error,omitempty"`
}

func validateTestPoolExhaustion(load *testLoadSpec) error {
	spec := load.PoolExhaustion
	if spec == nil {
		return nil
	}
	fail := func(message string) error { return fmt.Errorf("load.pool_exhaustion: %s", message) }
	if spec.Capacity < 1 || spec.Capacity > testLoadMaxVUs || spec.MaxQueue < 1 || spec.MaxQueue > testLoadMaxRequests || spec.MinQueuedRequests < 1 || spec.MinQueuedRequests > testLoadMaxRequests || spec.MinCompletions < 1 || spec.MinCompletions > testLoadMaxRequests || spec.MinCancelledWaiters < 0 || spec.MinCancelledWaiters > testLoadMaxRequests {
		return fail("requires bounded positive capacity, max_queue, min_queued_requests and min_completions, and nonnegative min_cancelled_waiters")
	}
	wait, err := time.ParseDuration(spec.MaxWait)
	if err != nil || wait < testConnectionPoll || wait > 5*time.Second || wait%time.Millisecond != 0 {
		return fail("max_wait must be whole milliseconds between 100ms and 5s")
	}
	window, err := time.ParseDuration(spec.Window)
	if err != nil || window < 300*time.Millisecond || window > 5*time.Second || window%time.Millisecond != 0 {
		return fail("window must be whole milliseconds between 300ms and 5s")
	}
	copyLoad := *load
	copyLoad.ConnectionLeaks = &testConnectionLeaksSpec{Path: spec.Path, As: spec.As, Within: spec.Within, Sustain: spec.Sustain}
	if err := validateTestConnectionLeaks(&copyLoad); err != nil {
		return fail(err.Error())
	}
	for i := 2; i < len(load.ChaosSchedule); i += 2 {
		if circuitDuration(load.ChaosSchedule[i].Duration) < circuitDuration(spec.Within)+circuitDuration(spec.Sustain)+window+2*testConnectionPoll {
			return fail("recovery must fit within, sustain, window and two polling intervals")
		}
	}
	return nil
}

func readTestPoolSnapshot(ctx context.Context, client *http.Client, baseURL string, spec *testPoolExhaustionSpec, keys map[string]string) (*testPoolSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+spec.Path, nil)
	if err != nil {
		return nil, errors.New("invalid pool observation URL")
	}
	if spec.As != "" {
		token := keys[spec.As]
		if token == "" {
			return nil, errors.New("pool observation consumer credential unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("pool observation request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pool observation returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return nil, errors.New("pool observation unreadable or exceeds 4 KiB")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, errors.New("pool observation must return one JSON object")
	}
	for _, field := range []string{"capacity", "active", "queued", "peak_active", "peak_queued", "waiters_created", "waiters_finished", "waiters_cancelled", "waiters_timed_out", "max_wait_ms", "completed"} {
		count, err := strconv.ParseInt(string(fields[field]), 10, 64)
		if err != nil || count < 0 || count > math.MaxInt32 {
			return nil, fmt.Errorf("pool observation requires nonnegative integer %s", field)
		}
	}
	var snapshot testPoolSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil, errors.New("invalid pool observation")
	}
	id, err := uuid.Parse(snapshot.Instance)
	if err != nil || id == uuid.Nil || id.String() != snapshot.Instance {
		return nil, errors.New("pool observation requires a canonical nonzero instance UUID")
	}
	if snapshot.Capacity == 0 || snapshot.Active > snapshot.Capacity || snapshot.PeakActive < snapshot.Active || snapshot.PeakActive > snapshot.Capacity || snapshot.PeakQueued < snapshot.Queued || snapshot.Created-snapshot.Finished != snapshot.Queued || snapshot.Finished < snapshot.Cancelled+snapshot.TimedOut {
		return nil, errors.New("pool observation accounting is inconsistent")
	}
	return &snapshot, nil
}

func poolSnapshotError(current, previous *testPoolSnapshot, spec *testPoolExhaustionSpec) string {
	if current == nil {
		return "pool observations unavailable"
	}
	if current.Capacity != spec.Capacity || current.Active > spec.Capacity || current.PeakActive > spec.Capacity || current.Queued > spec.MaxQueue || current.PeakQueued > spec.MaxQueue || current.MaxWaitMS > circuitDuration(spec.MaxWait).Milliseconds() {
		return "pool capacity, queue depth or wait budget exceeded"
	}
	if previous != nil {
		if current.Instance != previous.Instance {
			return "application instance changed during pool observation"
		}
		a := []int64{current.Created, current.Finished, current.Cancelled, current.TimedOut, current.Completed, current.PeakActive, current.PeakQueued, current.MaxWaitMS}
		b := []int64{previous.Created, previous.Finished, previous.Cancelled, previous.TimedOut, previous.Completed, previous.PeakActive, previous.PeakQueued, previous.MaxWaitMS}
		for i := range a {
			if a[i] < b[i] {
				return "pool observation counters reset or moved backwards"
			}
		}
	}
	return ""
}

func monitorTestPoolExhaustion(ctx context.Context, baseURL string, env []string, spec *testPoolExhaustionSpec, clock *testLoadPhaseClock) []testPoolSample {
	if spec == nil {
		return nil
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(testConnectionPoll)
	defer ticker.Stop()
	keys := testHTTPConsumerKeys(env)
	samples := []testPoolSample{}
	for {
		select {
		case <-ctx.Done():
			return samples
		case <-ticker.C:
		}
		at := time.Now()
		phase := clock.phaseAt(at)
		if phase < 0 {
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		snapshot, err := readTestPoolSnapshot(pollCtx, client, baseURL, spec, keys)
		cancel()
		if ctx.Err() != nil {
			return samples
		}
		if clock.phaseAt(time.Now()) != phase {
			continue
		}
		sample := testPoolSample{At: at, Phase: phase, Snapshot: snapshot}
		if err != nil {
			sample.Error = err.Error()
		}
		if len(samples) >= testConnectionSampleLimit {
			sample.Error = "pool observation capacity exceeded"
			samples[len(samples)-1] = sample
			return samples
		}
		samples = append(samples, sample)
	}
}

func evaluateTestPoolPhase(samples []testPoolSample, window testLoadPhaseWindow, spec *testPoolExhaustionSpec, recovery bool) *testPoolPhaseEvidence {
	e := &testPoolPhaseEvidence{Samples: len(samples)}
	fail := func(message string) *testPoolPhaseEvidence { e.Error = message; return e }
	times := []testConnectionSample{}
	for _, sample := range samples {
		times = append(times, testConnectionSample{At: sample.At, Error: sample.Error})
	}
	if !testConnectionBaselineComplete(times, window) {
		return fail("complete pool observation window unavailable")
	}
	for _, sample := range samples {
		if sample.Error != "" {
			return fail(sample.Error)
		}
		snapshot := sample.Snapshot
		if snapshot == nil {
			return fail("pool observations unavailable")
		}
		if snapshot.PeakActive > e.PeakActive {
			e.PeakActive = snapshot.PeakActive
		}
		if snapshot.PeakQueued > e.PeakQueued {
			e.PeakQueued = snapshot.PeakQueued
		}
		if snapshot.MaxWaitMS > e.MaxWaitMS {
			e.MaxWaitMS = snapshot.MaxWaitMS
		}
		if problem := poolSnapshotError(snapshot, nil, spec); problem != "" {
			return fail(problem)
		}
	}
	e.QueuedRequests = samples[len(samples)-1].Snapshot.Created - samples[0].Snapshot.Created
	if !recovery {
		if e.QueuedRequests < spec.MinQueuedRequests || e.PeakActive != spec.Capacity {
			return fail("pool exhaustion was not exercised by enough queued requests")
		}
		e.Passed = true
		return e
	}
	duration := circuitDuration(spec.Window)
	var healthy time.Time
	var healthyEnd time.Time
	begin := 0
	for end := range samples {
		target := samples[end].At.Add(-duration)
		for begin+1 < end && !samples[begin+1].At.After(target) {
			begin++
		}
		elapsed := samples[end].At.Sub(samples[begin].At)
		if elapsed < duration {
			continue
		}
		completed := samples[end].Snapshot.Completed - samples[begin].Snapshot.Completed
		required := int64(math.Ceil(float64(spec.MinCompletions) * float64(elapsed) / float64(duration)))
		row := testPoolThroughputWindow{StartMS: samples[begin].At.Sub(window.StartedAt).Milliseconds(), EndMS: samples[end].At.Sub(window.StartedAt).Milliseconds(), Completed: completed, Required: required, Passed: completed >= required}
		e.Windows = append(e.Windows, row)
		if !row.Passed {
			healthy = time.Time{}
			healthyEnd = time.Time{}
			e.RecoveryMS = nil
			continue
		}
		if healthy.IsZero() {
			healthy = samples[begin].At
			healthyEnd = samples[end].At
		}
		if healthy.Sub(window.StartedAt) <= circuitDuration(spec.Within) && samples[end].At.Sub(healthyEnd) >= circuitDuration(spec.Sustain) {
			ms := healthy.Sub(window.StartedAt).Milliseconds()
			e.RecoveryMS = &ms
		}
	}
	if e.RecoveryMS == nil {
		return fail("successful completion throughput did not recover within the deadline and remain sustained")
	}
	e.Passed = true
	return e
}

func checkTestPoolAfterDrain(ctx context.Context, baseURL string, env []string, spec *testPoolExhaustionSpec, baseline, last *testPoolSnapshot) *testPoolDrainEvidence {
	e := &testPoolDrainEvidence{}
	started := time.Now()
	within, sustain := circuitDuration(spec.Within), circuitDuration(spec.Sustain)
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), within+sustain+500*time.Millisecond)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	var healthy time.Time
	for {
		at := time.Now()
		pollCtx, stop := context.WithTimeout(drainCtx, 250*time.Millisecond)
		snapshot, err := readTestPoolSnapshot(pollCtx, client, baseURL, spec, testHTTPConsumerKeys(env))
		stop()
		e.Snapshot = snapshot
		if err != nil {
			e.Error = err.Error()
			return e
		}
		if problem := poolSnapshotError(snapshot, last, spec); problem != "" {
			e.Error = problem
			return e
		}
		last = snapshot
		e.CancelledWaiters = snapshot.Cancelled - baseline.Cancelled
		if snapshot.Active == 0 && snapshot.Queued == 0 && snapshot.Created == snapshot.Finished && e.CancelledWaiters >= spec.MinCancelledWaiters {
			if healthy.IsZero() {
				healthy = at
			}
		} else {
			healthy = time.Time{}
		}
		if !healthy.IsZero() && healthy.Sub(started) <= within && time.Since(healthy) >= sustain {
			e.Passed = true
			return e
		}
		if time.Since(started) > within && (healthy.IsZero() || healthy.Sub(started) > within) {
			e.Error = "pool permits or queued waiters did not drain within the deadline, or required cancellation evidence is missing"
			return e
		}
		timer := time.NewTimer(testConnectionPoll)
		select {
		case <-drainCtx.Done():
			timer.Stop()
			e.Error = "post-drain pool observation deadline exceeded"
			return e
		case <-timer.C:
		}
	}
}

func applyTestPoolExhaustion(ctx context.Context, baseURL string, env []string, report *testLoadEvidence, cfg *testLoadConfig, clock *testLoadPhaseClock, samples []testPoolSample, scheduleErr error) error {
	spec := cfg.PoolExhaustion
	if spec == nil {
		return nil
	}
	e := &testPoolExhaustionEvidence{Source: "application", Budget: spec, Cancelled: ctx.Err() != nil}
	report.PoolExhaustion = e
	var failures []error
	phases := make([][]testPoolSample, len(cfg.ChaosSchedule))
	var previous *testPoolSnapshot
	for _, sample := range samples {
		if sample.Phase < 0 || sample.Phase >= len(phases) {
			failures = append(failures, errors.New("pool observation phase invalid"))
			continue
		}
		if sample.Snapshot != nil {
			if sample.Snapshot.PeakActive > e.PeakActive {
				e.PeakActive = sample.Snapshot.PeakActive
			}
			if sample.Snapshot.PeakQueued > e.PeakQueued {
				e.PeakQueued = sample.Snapshot.PeakQueued
			}
			if sample.Snapshot.MaxWaitMS > e.MaxWaitMS {
				e.MaxWaitMS = sample.Snapshot.MaxWaitMS
			}
		}
		if sample.Error == "" {
			sample.Error = poolSnapshotError(sample.Snapshot, previous, spec)
		}
		if sample.Error != "" {
			failures = append(failures, errors.New(sample.Error))
		} else {
			previous = sample.Snapshot
			if sample.Phase == 0 && e.Baseline == nil {
				e.Baseline = sample.Snapshot
			}
			if sample.Snapshot.PeakActive > e.PeakActive {
				e.PeakActive = sample.Snapshot.PeakActive
			}
			if sample.Snapshot.PeakQueued > e.PeakQueued {
				e.PeakQueued = sample.Snapshot.PeakQueued
			}
			if sample.Snapshot.MaxWaitMS > e.MaxWaitMS {
				e.MaxWaitMS = sample.Snapshot.MaxWaitMS
			}
		}
		phases[sample.Phase] = append(phases[sample.Phase], sample)
	}
	times := []testConnectionSample{}
	for _, sample := range phases[0] {
		times = append(times, testConnectionSample{At: sample.At, Error: sample.Error})
	}
	if e.Baseline == nil || !testConnectionBaselineComplete(times, clock.window(0)) {
		e.Baseline = nil
		failures = append(failures, errors.New("pool baseline unavailable"))
	}
	for i := 1; i < len(cfg.ChaosSchedule); i++ {
		phase := evaluateTestPoolPhase(phases[i], clock.window(i), spec, i%2 == 0)
		if i < len(report.ChaosPhases) {
			report.ChaosPhases[i].PoolExhaustion = phase
		}
		if !phase.Passed {
			if e.FirstFailedCycle == 0 {
				e.FirstFailedCycle = (i + 1) / 2
			}
			failures = append(failures, fmt.Errorf("phase %q pool exhaustion: %s", cfg.ChaosSchedule[i].Name, phase.Error))
		}
	}
	if e.Baseline != nil {
		e.PostDrain = checkTestPoolAfterDrain(ctx, baseURL, env, spec, e.Baseline, previous)
		if !e.PostDrain.Passed {
			failures = append(failures, errors.New(e.PostDrain.Error))
		}
	}
	if scheduleErr != nil {
		failures = append(failures, errors.New("pool campaign schedule incomplete"))
	}
	err := errors.Join(failures...)
	e.Passed = err == nil
	if err != nil {
		e.Error = err.Error()
		report.Status = "failed"
	}
	return err
}

func printTestPoolExhaustion(out io.Writer, report *testLoadEvidence) {
	if report == nil || report.PoolExhaustion == nil {
		return
	}
	e := report.PoolExhaustion
	_, _ = fmt.Fprintf(out, "  application pool exhaustion: passed %t; peak active %d, peak queued %d, maximum wait %dms; first failed cycle %d; %s\n", e.Passed, e.PeakActive, e.PeakQueued, e.MaxWaitMS, e.FirstFailedCycle, e.Error)
	for _, phase := range report.ChaosPhases {
		if p := phase.PoolExhaustion; p != nil {
			_, _ = fmt.Fprintf(out, "    %s: queued %d requests; %d throughput windows; passed %t; %s\n", phase.Name, p.QueuedRequests, len(p.Windows), p.Passed, p.Error)
		}
	}
	if e.PostDrain != nil {
		_, _ = fmt.Fprintf(out, "    post-drain pool: passed %t; cancelled waiters %d; %s\n", e.PostDrain.Passed, e.PostDrain.CancelledWaiters, e.PostDrain.Error)
	}
}
