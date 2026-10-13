package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const testConnectionPoll = 100 * time.Millisecond
const testConnectionSampleLimit = 4096

type testConnectionLeaksSpec struct {
	Path     string `yaml:"path" json:"path"`
	As       string `yaml:"as,omitempty" json:"as,omitempty"`
	Within   string `yaml:"within" json:"within"`
	Sustain  string `yaml:"sustain" json:"sustain"`
	MaxExtra int64  `yaml:"max_extra,omitempty" json:"max_extra,omitempty"`
}

type testConnectionSample struct {
	At       time.Time
	Phase    int
	Active   int64
	Instance string
	Error    string
}
type testConnectionLeakEvidence struct {
	Passed     bool   `json:"passed"`
	Baseline   int64  `json:"baseline"`
	Peak       int64  `json:"peak"`
	Remaining  int64  `json:"remaining"`
	Limit      int64  `json:"limit"`
	Samples    int    `json:"samples"`
	RecoveryMS *int64 `json:"recovery_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}
type testConnectionLeaksEvidence struct {
	WithinMS         int64                       `json:"within_ms"`
	SustainMS        int64                       `json:"sustain_ms"`
	MaxExtra         int64                       `json:"max_extra"`
	Samples          int                         `json:"samples"`
	Source           string                      `json:"source"`
	Path             string                      `json:"path"`
	Instance         string                      `json:"instance_id,omitempty"`
	Baseline         *int64                      `json:"baseline,omitempty"`
	Peak             int64                       `json:"peak"`
	FirstFailedCycle int                         `json:"first_failed_cycle,omitempty"`
	Cancelled        bool                        `json:"cancelled"`
	Passed           bool                        `json:"passed"`
	PostDrain        *testConnectionLeakEvidence `json:"post_drain,omitempty"`
	Error            string                      `json:"error,omitempty"`
}

func testConnectionBaselineText(value *int64) string {
	if value == nil {
		return "unconfirmed"
	}
	return strconv.FormatInt(*value, 10)
}

func validateTestConnectionLeaks(load *testLoadSpec) error {
	spec := load.ConnectionLeaks
	if spec == nil {
		return nil
	}
	fail := func(message string) error { return fmt.Errorf("load.connection_leaks: %s", message) }
	parsed, err := url.Parse(spec.Path)
	if err != nil || !strings.HasPrefix(spec.Path, "/") || strings.HasPrefix(spec.Path, "//") || parsed.Host != "" || parsed.Fragment != "" || strings.ContainsAny(spec.Path, "${\\\r\n") {
		return fail("path must be a fixed application-relative path")
	}
	if spec.As != "" && !testHTTPNamePattern.MatchString(spec.As) {
		return fail("as must name a valid consumer")
	}
	within, e1 := time.ParseDuration(spec.Within)
	sustain, e2 := time.ParseDuration(spec.Sustain)
	if e1 != nil || e2 != nil || within < 300*time.Millisecond || sustain < 300*time.Millisecond || within > 5*time.Second || sustain > 5*time.Second || within%time.Millisecond != 0 || sustain%time.Millisecond != 0 || spec.MaxExtra < 0 || spec.MaxExtra > math.MaxInt32 {
		return fail("within and sustain must be whole milliseconds between 300ms and 5s; max_extra must be a nonnegative int32")
	}
	phases := load.ChaosSchedule
	if len(phases) < 3 || len(phases[0].Rules) != 0 || circuitDuration(phases[0].Duration) < 500*time.Millisecond {
		return fail("requires a baseline of at least 500ms followed by TCP fault/recovery pairs")
	}
	for i := 1; i < len(phases); i += 2 {
		if i+1 >= len(phases) || len(phases[i].Rules) != 1 || !(strings.HasPrefix(phases[i].Rules[0].Kind, "tcp_")) || phases[i].Rules[0].From == "" || len(phases[i+1].Rules) != 0 || circuitDuration(phases[i+1].Duration) < within+sustain+2*testConnectionPoll {
			return fail("requires one source-scoped TCP fault per pair and recovery long enough for within, sustain and two polls")
		}
	}
	return nil
}

func readTestConnections(ctx context.Context, client *http.Client, baseURL string, spec *testConnectionLeaksSpec, keys map[string]string) (int64, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+spec.Path, nil)
	if err != nil {
		return 0, "", errors.New("invalid connection observation URL")
	}
	if spec.As != "" {
		token := keys[spec.As]
		if token == "" {
			return 0, "", errors.New("connection observation consumer credential unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", errors.New("connection observation request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("connection observation returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return 0, "", errors.New("connection observation unreadable or exceeds 4 KiB")
	}
	var payload struct {
		Instance string `json:"instance_id"`
		Active   *int64 `json:"active_connections"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || payload.Active == nil || *payload.Active < 0 || *payload.Active > math.MaxInt32 {
		return 0, "", errors.New("connection observation requires a single JSON object with nonnegative integer active_connections")
	}
	id, err := uuid.Parse(payload.Instance)
	if err != nil || id.String() != payload.Instance || id == uuid.Nil {
		return 0, "", errors.New("connection observation requires a canonical nonzero instance UUID")
	}
	return *payload.Active, payload.Instance, nil
}

func monitorTestConnectionLeaks(ctx context.Context, baseURL string, env []string, spec *testConnectionLeaksSpec, clock *testLoadPhaseClock) []testConnectionSample {
	if spec == nil {
		return nil
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	keys := testHTTPConsumerKeys(env)
	ticker := time.NewTicker(testConnectionPoll)
	defer ticker.Stop()
	samples := []testConnectionSample{}
	for {
		select {
		case <-ctx.Done():
			return samples
		case <-ticker.C:
		}
		started := time.Now()
		phase := clock.phaseAt(started)
		if phase < 0 {
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		count, id, err := readTestConnections(pollCtx, client, baseURL, spec, keys)
		cancel()
		if ctx.Err() != nil {
			return samples
		}
		// A response spanning a transition is not attributed to either phase.
		if clock.phaseAt(time.Now()) != phase {
			continue
		}
		sample := testConnectionSample{At: started, Phase: phase, Active: count, Instance: id}
		if err != nil {
			sample.Error = err.Error()
		}
		if len(samples) >= testConnectionSampleLimit {
			sample.Error = "connection observation capacity exceeded"
			samples[len(samples)-1] = sample
			return samples
		}
		samples = append(samples, sample)
	}
}

func evaluateTestConnectionWindow(samples []testConnectionSample, window testLoadPhaseWindow, baseline, limit int64, within, sustain time.Duration) *testConnectionLeakEvidence {
	e := &testConnectionLeakEvidence{Baseline: baseline, Limit: limit, Samples: len(samples)}
	fail := func(message string) *testConnectionLeakEvidence { e.Error = message; return e }
	if window.StartedAt.IsZero() || !window.FinishedAt.After(window.StartedAt) || len(samples) < 3 {
		return fail("complete connection observation window unavailable")
	}
	var healthy time.Time
	for i, sample := range samples {
		if sample.Error != "" {
			return fail(sample.Error)
		}
		if sample.At.Before(window.StartedAt) || !sample.At.Before(window.FinishedAt) || (i > 0 && (sample.At.Sub(samples[i-1].At) > 350*time.Millisecond || !sample.At.After(samples[i-1].At))) {
			return fail("connection observation timestamps missing, unordered or too sparse")
		}
		if sample.Active > e.Peak {
			e.Peak = sample.Active
		}
		e.Remaining = sample.Active
		if sample.Active > limit {
			healthy = time.Time{}
			e.RecoveryMS = nil
			continue
		}
		if healthy.IsZero() {
			healthy = sample.At
		}
		if healthy.Sub(window.StartedAt) <= within && sample.At.Sub(healthy) >= sustain {
			ms := healthy.Sub(window.StartedAt).Milliseconds()
			e.RecoveryMS = &ms
		}
	}
	if samples[0].At.Sub(window.StartedAt) > 350*time.Millisecond || window.FinishedAt.Sub(samples[len(samples)-1].At) > 350*time.Millisecond {
		return fail("connection observation does not cover the complete recovery window")
	}
	if e.RecoveryMS == nil {
		return fail("connections did not return to baseline budget within the deadline and stay within budget")
	}
	e.Passed = true
	return e
}

func checkTestConnectionsAfterDrain(ctx context.Context, baseURL string, env []string, spec *testConnectionLeaksSpec, baseline int64, instance string) *testConnectionLeakEvidence {
	within, sustain := circuitDuration(spec.Within), circuitDuration(spec.Sustain)
	started := time.Now()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), within+sustain+500*time.Millisecond)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	e := &testConnectionLeakEvidence{Baseline: baseline, Limit: baseline}
	var healthy time.Time
	keys := testHTTPConsumerKeys(env)
	for {
		at := time.Now()
		pollCtx, stop := context.WithTimeout(cleanupCtx, 250*time.Millisecond)
		count, id, err := readTestConnections(pollCtx, client, baseURL, spec, keys)
		stop()
		e.Samples++
		e.Remaining = count
		if count > e.Peak {
			e.Peak = count
		}
		if err != nil {
			e.Error = err.Error()
			return e
		}
		if id != instance {
			e.Error = "application instance changed during connection observation"
			return e
		}
		if count > baseline {
			healthy = time.Time{}
		} else if healthy.IsZero() {
			healthy = at
		}
		if !healthy.IsZero() && healthy.Sub(started) <= within && time.Since(healthy) >= sustain && e.Samples >= 3 {
			e.Passed = true
			ms := healthy.Sub(started).Milliseconds()
			e.RecoveryMS = &ms
			return e
		}
		if time.Since(started) > within && (healthy.IsZero() || healthy.Sub(started) > within) {
			e.Error = "post-drain connections did not return to the initial baseline within the deadline"
			return e
		}
		timer := time.NewTimer(testConnectionPoll)
		select {
		case <-cleanupCtx.Done():
			timer.Stop()
			e.Error = "post-drain connection observation deadline exceeded"
			return e
		case <-timer.C:
		}
	}
}

func testConnectionBaselineComplete(samples []testConnectionSample, window testLoadPhaseWindow) bool {
	if len(samples) < 3 || window.StartedAt.IsZero() || !window.FinishedAt.After(window.StartedAt) {
		return false
	}
	for i, sample := range samples {
		if sample.Error != "" || sample.At.Before(window.StartedAt) || !sample.At.Before(window.FinishedAt) || (i > 0 && (!sample.At.After(samples[i-1].At) || sample.At.Sub(samples[i-1].At) > 350*time.Millisecond)) {
			return false
		}
	}
	return samples[0].At.Sub(window.StartedAt) <= 350*time.Millisecond && window.FinishedAt.Sub(samples[len(samples)-1].At) <= 350*time.Millisecond
}

func applyTestConnectionLeaks(ctx context.Context, baseURL string, env []string, report *testLoadEvidence, cfg *testLoadConfig, clock *testLoadPhaseClock, samples []testConnectionSample, scheduleErr error) error {
	spec := cfg.ConnectionLeaks
	if spec == nil {
		return nil
	}
	e := &testConnectionLeaksEvidence{Source: "application", Path: spec.Path, Cancelled: ctx.Err() != nil, WithinMS: circuitDuration(spec.Within).Milliseconds(), SustainMS: circuitDuration(spec.Sustain).Milliseconds(), MaxExtra: spec.MaxExtra, Samples: len(samples)}
	report.ConnectionLeaks = e
	var failures []error
	phases := make([][]testConnectionSample, len(cfg.ChaosSchedule))
	baselineWindow := clock.window(0)
	for _, sample := range samples {
		if sample.Phase < 0 || sample.Phase >= len(phases) {
			failures = append(failures, errors.New("connection observation phase invalid"))
			continue
		}
		if sample.Error == "" && e.Instance != "" && sample.Instance != e.Instance {
			sample.Error = "application instance changed during connection observation"
		}
		phases[sample.Phase] = append(phases[sample.Phase], sample)
		if sample.Error != "" {
			failures = append(failures, errors.New(sample.Error))
			continue
		}
		if sample.Active > e.Peak {
			e.Peak = sample.Active
		}
		if e.Instance == "" {
			e.Instance = sample.Instance
		} else if e.Instance != sample.Instance {
			failures = append(failures, errors.New("application instance changed during connection observation"))
		}
		if sample.Phase == 0 && !sample.At.Before(baselineWindow.StartedAt) && sample.At.Before(baselineWindow.FinishedAt) && (e.Baseline == nil || sample.Active < *e.Baseline) {
			value := sample.Active
			e.Baseline = &value
		}
	}
	if e.Baseline == nil || !testConnectionBaselineComplete(phases[0], baselineWindow) {
		failures = append(failures, errors.New("connection baseline unavailable"))
		e.Baseline = nil
	}
	if e.Baseline != nil {
		for i := 2; i < len(cfg.ChaosSchedule); i += 2 {
			observed := evaluateTestConnectionWindow(phases[i], clock.window(i), *e.Baseline, *e.Baseline+spec.MaxExtra, circuitDuration(spec.Within), circuitDuration(spec.Sustain))
			if len(phases[i-1]) < 3 {
				observed.Passed = false
				observed.Error = "fault connection observations unavailable"
			}
			for _, sample := range phases[i-1] {
				if sample.Error != "" {
					observed.Passed = false
					observed.Error = sample.Error
				}
				if sample.Active > observed.Peak {
					observed.Peak = sample.Active
				}
			}
			if i < len(report.ChaosPhases) {
				report.ChaosPhases[i].ConnectionLeaks = observed
			}
			if !observed.Passed {
				if e.FirstFailedCycle == 0 {
					e.FirstFailedCycle = i / 2
				}
				failures = append(failures, fmt.Errorf("phase %q connection leaks: %s", cfg.ChaosSchedule[i].Name, observed.Error))
			}
		}
		// Load workers and schedule cleanup have joined before this independent check.
		e.PostDrain = checkTestConnectionsAfterDrain(ctx, baseURL, env, spec, *e.Baseline, e.Instance)
		if e.PostDrain.Peak > e.Peak {
			e.Peak = e.PostDrain.Peak
		}
		if !e.PostDrain.Passed {
			failures = append(failures, errors.New(e.PostDrain.Error))
		}
	}
	if scheduleErr != nil {
		failures = append(failures, errors.New("connection campaign schedule incomplete"))
	}
	err := errors.Join(failures...)
	e.Passed = err == nil
	if err != nil {
		e.Error = err.Error()
		report.Status = "failed"
	}
	return err
}
