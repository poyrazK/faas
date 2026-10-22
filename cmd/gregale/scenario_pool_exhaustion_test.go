package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func poolTestSpec() *main.TestPoolExhaustionSpec {
	return &main.TestPoolExhaustionSpec{Path: "/pool-status", Capacity: 2, MaxQueue: 2, MaxWait: "300ms", MinQueuedRequests: 3, Within: "1s", Sustain: "1s", Window: "1s", MinCompletions: 5}
}
func poolTestSnapshot() *main.TestPoolSnapshot {
	return &main.TestPoolSnapshot{Instance: connectionTestInstance, Capacity: 2, PeakActive: 2, PeakQueued: 2, Created: 20, Finished: 20, MaxWaitMS: 200, Completed: 100}
}
func poolTestSamples(start time.Time, phase int, count int) []main.TestPoolSample {
	list := make([]main.TestPoolSample, count)
	for i := range list {
		snapshot := poolTestSnapshot()
		snapshot.Completed = int64(100 + i*2)
		list[i] = main.TestPoolSample{At: start.Add(time.Duration(i+1) * 100 * time.Millisecond), Phase: phase, Snapshot: snapshot}
	}
	return list
}

func TestPoolExhaustionRejectsMissingAccountingAndCounterResets(t *testing.T) {
	valid, _ := json.Marshal(poolTestSnapshot())
	var original map[string]any
	_ = json.Unmarshal(valid, &original)
	for _, field := range []string{"capacity", "active", "queued", "peak_active", "peak_queued", "waiters_created", "waiters_finished", "waiters_cancelled", "waiters_timed_out", "max_wait_ms", "completed", "instance_id"} {
		for _, value := range []any{nil, "invalid", -1, 1.5} {
			t.Run(field+" invalid", func(t *testing.T) {
				payload := map[string]any{}
				for key, v := range original {
					payload[key] = v
				}
				if value == nil {
					delete(payload, field)
				} else {
					payload[field] = value
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(payload) }))
				defer server.Close()
				if _, err := main.ReadTestPoolSnapshot(context.Background(), server.Client(), server.URL, poolTestSpec(), nil); err == nil {
					t.Fatalf("accepted missing/invalid %s", field)
				}
			})
		}
	}
	baseline := poolTestSnapshot()
	for _, change := range []func(*main.TestPoolSnapshot){func(s *main.TestPoolSnapshot) { s.Created-- }, func(s *main.TestPoolSnapshot) { s.Completed = 0 }, func(s *main.TestPoolSnapshot) { s.Instance = "11234567-89ab-4cde-8fab-0123456789ab" }, func(s *main.TestPoolSnapshot) { s.MaxWaitMS = 301 }, func(s *main.TestPoolSnapshot) { s.PeakQueued = 3 }} {
		current := *baseline
		change(&current)
		if main.PoolSnapshotError(&current, baseline, poolTestSpec()) == "" {
			t.Fatal("reset, restart or exceeded budget passed")
		}
	}
}

func TestPoolExhaustionThroughputAndRealExhaustionRequired(t *testing.T) {
	start := time.Now()
	window := main.TestLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(4 * time.Second)}
	for _, tc := range []struct {
		name   string
		change func([]main.TestPoolSample)
		pass   bool
	}{{"recovered", func([]main.TestPoolSample) {}, true}, {"slow recovery", func(s []main.TestPoolSample) {
		for i := range s {
			s[i].Snapshot.Completed = 100
		}
	}, false}, {"late relapse", func(s []main.TestPoolSample) {
		for i := 25; i < len(s); i++ {
			s[i].Snapshot.Completed = s[24].Snapshot.Completed
		}
	}, false}, {"queue overflow", func(s []main.TestPoolSample) { s[3].Snapshot.PeakQueued = 3 }, false}, {"long wait", func(s []main.TestPoolSample) { s[3].Snapshot.MaxWaitMS = 301 }, false}, {"missing samples", func(s []main.TestPoolSample) { s[8].Error = "missing" }, false}} {
		t.Run(tc.name, func(t *testing.T) {
			samples := poolTestSamples(start, 2, 39)
			tc.change(samples)
			e := main.EvaluateTestPoolPhase(samples, window, poolTestSpec(), true)
			if e.Passed != tc.pass {
				t.Fatalf("%+v", e)
			}
		})
	}
	samples := poolTestSamples(start, 1, 19)
	faultWindow := main.TestLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	if e := main.EvaluateTestPoolPhase(samples, faultWindow, poolTestSpec(), false); e.Passed {
		t.Fatal("quiet pool passed as exhausted")
	}
	for i := range samples {
		samples[i].Snapshot.Created += int64(i)
		samples[i].Snapshot.Finished += int64(i)
	}
	if e := main.EvaluateTestPoolPhase(samples, faultWindow, poolTestSpec(), false); !e.Passed || e.QueuedRequests != 18 {
		t.Fatalf("actual exhaustion: %+v", e)
	}
}

func TestPoolExhaustionManifestReplayAndReports(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-checkout/gregale-pool-exhaustion-test.yaml", "pool-exhaustion", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("manifest: %v", err)
	}
	source := prepared[0].Scenario
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{prepared[0].Name: source}, prepared[0].ManifestDir, prepared[0].Name, prepared[0].Engine); err != nil {
		t.Fatal(err)
	}
	cfg := main.OutageCyclesConfig(t, source)
	clock := main.NewTestLoadPhaseClock(3)
	at := time.Now().Add(-time.Minute)
	samples := []main.TestPoolSample{}
	for i, phase := range cfg.ChaosSchedule {
		(*clock.Windows())[i] = main.TestLoadPhaseWindow{StartedAt: at, FinishedAt: at.Add(phase.Duration)}
		list := poolTestSamples(at, i, int(phase.Duration/main.TestConnectionPoll)-1)
		for j := range list {
			s := list[j].Snapshot
			if i == 0 {
				s.Created, s.Finished, s.Completed, s.PeakActive, s.PeakQueued, s.MaxWaitMS = 0, 0, int64(j), 1, 0, 0
			} else if i == 1 {
				s.Created, s.Finished, s.Completed = int64(j), int64(j), 19
			} else {
				s.Created, s.Finished, s.Completed = 18, 18, int64(19+j*2)
			}
		}
		samples = append(samples, list...)
		at = at.Add(phase.Duration)
	}
	final := poolTestSnapshot()
	final.Created, final.Finished = 18, 18
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(final) }))
	defer server.Close()
	report := main.OutageCyclesReceipt(t, cfg)
	if err := main.ApplyTestPoolExhaustion(context.Background(), server.URL, nil, report, cfg, clock, samples, nil); err != nil || !report.PoolExhaustion.Passed {
		t.Fatalf("pool result: %+v %v", report.PoolExhaustion, err)
	}
	receipt := main.TestRunReceipt{Scenario: "pool-exhaustion", Status: "passed", Load: report, Steps: []main.TestScenarioStepEvidence{{Name: "load", Load: report}}}
	var terminal bytes.Buffer
	main.PrintTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "peak queued 2") {
		t.Fatal("terminal lost queue evidence")
	}
	html := filepath.Join(t.TempDir(), "results.html")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !bytes.Contains(body, []byte("Application pool exhaustion")) || !bytes.Contains(body, []byte("Throughput")) {
		t.Fatal("HTML lost pool evidence")
	}
	junit := filepath.Join(t.TempDir(), "results.xml")
	if err := main.WriteTestJUnit(junit, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(junit)
	var suite main.TestJUnitSuite
	_ = xml.Unmarshal(body, &suite)
	var decoded main.TestRunReceipt
	_ = json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded)
	if decoded.Load.PoolExhaustion == nil || !decoded.Load.PoolExhaustion.Passed || len(decoded.Load.ChaosPhases[2].PoolExhaustion.Windows) == 0 {
		t.Fatal("JSON/JUnit lost measured throughput")
	}
	replay := main.TestRunReceipt{Scenario: "pool-exhaustion", Engine: "real-vm", Profile: "warm", Status: "failed", RunID: "pool", Replay: main.NewTestReplayManifest("pool-exhaustion", "warm", "", source)}
	altered := source
	altered.Steps = append([]main.TestScenarioStep(nil), source.Steps...)
	load := *source.Steps[1].Load
	budget := *load.PoolExhaustion
	budget.MaxQueue++
	load.PoolExhaustion = &budget
	altered.Steps[1].Load = &load
	if _, err := main.PrepareTestReplay(&replay, map[string]main.TestScenario{"pool-exhaustion": altered}, prepared[0].ManifestDir); err == nil {
		t.Fatal("changed queue budget escaped replay binding")
	}
}

func TestPoolExhaustionCancellationRequiresDrainedQueueAndEvidence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		queued    int64
		cancelled int64
		want      bool
	}{{"cancelled", 0, 2, true}, {"retained waiter", 1, 1, false}, {"missing cancellation", 0, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			spec := poolTestSpec()
			spec.Within, spec.Sustain = "300ms", "300ms"
			spec.MinCancelledWaiters = 2
			baseline := poolTestSnapshot()
			baseline.Cancelled = 0
			final := *baseline
			final.Cancelled = tc.cancelled
			final.Queued = tc.queued
			final.Created += tc.queued
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(final) }))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			e := main.CheckTestPoolAfterDrain(ctx, server.URL, nil, spec, baseline, baseline)
			if e.Passed != tc.want {
				t.Fatalf("post-cancellation: %+v", e)
			}
		})
	}
}
