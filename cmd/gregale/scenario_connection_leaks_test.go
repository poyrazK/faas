package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

const connectionTestInstance = "01234567-89ab-4cde-8fab-0123456789ab"

func TestConnectionLeakObservationRejectsIncompleteCounters(t *testing.T) {
	for _, body := range []string{`{}`, `{"instance_id":"` + connectionTestInstance + `"}`, `{"instance_id":"` + connectionTestInstance + `","active_connections":null}`, `{"instance_id":"` + connectionTestInstance + `","active_connections":-1}`, `{"instance_id":"` + connectionTestInstance + `","active_connections":1.5}`, `{"instance_id":"` + connectionTestInstance + `","active_connections":"1"}`, `{"instance_id":"` + connectionTestInstance + `","active_connections":2147483648}`, `{"instance_id":"changed","active_connections":0}`, `{"instance_id":"00000000-0000-0000-0000-000000000000","active_connections":0}`, strings.Repeat("x", 4097), `{"instance_id":"` + connectionTestInstance + `","active_connections":0} {}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, _, err := readTestConnections(context.Background(), server.Client(), server.URL, &testConnectionLeaksSpec{Path: "/connections"}, nil)
		server.Close()
		if err == nil {
			t.Fatalf("accepted incomplete counter: %q", body)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer credential" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": connectionTestInstance, "active_connections": 2, "extra": "allowed"})
	}))
	defer server.Close()
	count, id, err := readTestConnections(context.Background(), server.Client(), server.URL, &testConnectionLeaksSpec{Path: "/connections", As: "observer"}, map[string]string{"observer": "credential"})
	if err != nil || count != 2 || id != connectionTestInstance {
		t.Fatalf("valid observation: %d %s %v", count, id, err)
	}
}

func connectionWindowSamples(start time.Time, count int, active int64) []testConnectionSample {
	samples := make([]testConnectionSample, count)
	for i := range samples {
		samples[i] = testConnectionSample{At: start.Add(time.Duration(i+1) * 100 * time.Millisecond), Active: active, Instance: connectionTestInstance}
	}
	return samples
}

func TestConnectionLeakRecoveryRejectsLateRelapseAndSparseEvidence(t *testing.T) {
	start := time.Now()
	window := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	for _, tc := range []struct {
		name   string
		change func([]testConnectionSample)
		pass   bool
	}{{"bounded", func([]testConnectionSample) {}, true}, {"leak", func(s []testConnectionSample) {
		for i := range s {
			s[i].Active = 2
		}
	}, false}, {"late recovery", func(s []testConnectionSample) {
		for i := 0; i < 12; i++ {
			s[i].Active = 2
		}
	}, false}, {"relapse", func(s []testConnectionSample) { s[17].Active = 2 }, false}, {"sparse", func(s []testConnectionSample) { s[8].At = s[4].At }, false}, {"missing", func(s []testConnectionSample) { s[8].Error = "observation unavailable" }, false}} {
		t.Run(tc.name, func(t *testing.T) {
			samples := connectionWindowSamples(start, 19, 1)
			tc.change(samples)
			e := evaluateTestConnectionWindow(samples, window, 1, 1, time.Second, 500*time.Millisecond)
			if e.Passed != tc.pass {
				t.Fatalf("%+v", e)
			}
		})
	}
	// A warmed connection pool is the baseline, rather than an assumed zero.
	e := evaluateTestConnectionWindow(connectionWindowSamples(start, 19, 3), window, 3, 3, time.Second, 500*time.Millisecond)
	if !e.Passed || e.Baseline != 3 || e.Remaining != 3 {
		t.Fatalf("pool baseline: %+v", e)
	}
}

func TestConnectionLeakBudgetValidationAndReplayBinding(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-checkout/gregale-tcp-campaign-test.yaml", "tcp-campaign", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	source := prepared[0].Scenario
	for _, change := range []func(*testLoadSpec){func(l *testLoadSpec) { l.ConnectionLeaks.Path = "//external.example/" }, func(l *testLoadSpec) { l.ConnectionLeaks.Path = "/${env.secret}" }, func(l *testLoadSpec) { l.ConnectionLeaks.MaxExtra = -1 }, func(l *testLoadSpec) { l.ConnectionLeaks.Within = "6s" }, func(l *testLoadSpec) { l.ConnectionLeaks.Sustain = "1ms" }, func(l *testLoadSpec) { l.ChaosSchedule[2].Duration = "1s" }, func(l *testLoadSpec) { l.ChaosSchedule[1].Rules[0].From = "" }} {
		// Preserve internal expansion state while changing only the public budget.
		l := *source.Steps[1].Load
		budget := *l.ConnectionLeaks
		l.ConnectionLeaks = &budget
		l.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), l.ChaosSchedule...)
		for i := range l.ChaosSchedule {
			l.ChaosSchedule[i].Rules = append([]testChaosRule(nil), l.ChaosSchedule[i].Rules...)
		}
		change(&l)
		if err := validateTestLoadSpec(&l); err == nil {
			t.Fatal("invalid connection leak budget accepted")
		}
	}
	row := testRunReceipt{Scenario: "tcp-campaign", Engine: "real-vm", Profile: "warm", Status: "failed", RunID: "connection-leaks", Replay: newTestReplayManifest("tcp-campaign", "warm", "", source)}
	copyScenario := source
	copyScenario.Steps = append([]testScenarioStep(nil), source.Steps...)
	load := *source.Steps[1].Load
	budget := *load.ConnectionLeaks
	budget.MaxExtra++
	load.ConnectionLeaks = &budget
	copyScenario.Steps[1].Load = &load
	if _, err := prepareTestReplay(&row, map[string]testScenario{"tcp-campaign": copyScenario}, prepared[0].ManifestDir); err == nil {
		t.Fatal("changed connection budget escaped replay fingerprint")
	}
}

func TestConnectionLeakCountersDoNotRebaseLaterCyclesAndReports(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-checkout/gregale-tcp-campaign-test.yaml", "tcp-campaign", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	cfg := outageCyclesConfig(t, prepared[0].Scenario)
	start := time.Now().Add(-time.Minute)
	clock := newTestLoadPhaseClock(len(cfg.ChaosSchedule))
	at := start
	samples := []testConnectionSample{}
	for i, phase := range cfg.ChaosSchedule {
		clock.windows[i] = testLoadPhaseWindow{StartedAt: at, FinishedAt: at.Add(phase.Duration)}
		value := int64(0)
		if i > 0 && i%2 == 1 {
			value = 2
		}
		if i == 6 {
			value = 3
		}
		list := connectionWindowSamples(at, int(phase.Duration/testConnectionPoll)-1, value)
		for j := range list {
			list[j].Phase = i
		}
		samples = append(samples, list...)
		at = at.Add(phase.Duration)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": connectionTestInstance, "active_connections": 0})
	}))
	defer server.Close()
	report := outageCyclesReceipt(t, cfg)
	err = applyTestConnectionLeaks(context.Background(), server.URL, nil, report, cfg, clock, samples, nil)
	if err == nil || report.ConnectionLeaks.FirstFailedCycle != 3 || *report.ConnectionLeaks.Baseline != 0 || report.ConnectionLeaks.Peak != 3 || !report.ConnectionLeaks.PostDrain.Passed {
		t.Fatalf("leak evidence: %+v %v", report.ConnectionLeaks, err)
	}
	for i := 1; i < len(report.ChaosPhases); i += 2 {
		report.ChaosPhases[i].ConnectionAttempts = &testRetryAmplificationEvidence{Passed: true}
	}
	if err := applyTestOutageCycles(report, cfg, nil); err == nil || report.OutageCycles.FirstFailedCycle != 3 {
		t.Fatalf("cycle resource failure lost: %+v %v", report.OutageCycles, err)
	}
	receipt := testRunReceipt{Scenario: "tcp-campaign", Status: "failed", Load: report, Steps: []testScenarioStepEvidence{{Name: "fault-campaign", Load: report}}}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "baseline 0") || !strings.Contains(terminal.String(), "remaining 3") {
		t.Fatal("terminal lost counts")
	}
	html := filepath.Join(t.TempDir(), "results.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !bytes.Contains(body, []byte("sampled peak 3")) || !bytes.Contains(body, []byte("baseline 0")) {
		t.Fatal("HTML lost baseline or peak")
	}
	junit := filepath.Join(t.TempDir(), "results.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(junit)
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Load.ConnectionLeaks == nil || decoded.Load.ConnectionLeaks.Peak != 3 || decoded.Load.ConnectionLeaks.FirstFailedCycle != 3 {
		t.Fatal("JUnit/JSON lost resource evidence")
	}
}

func TestConnectionLeakCancellationClearsPlanAndChecksRetainedSockets(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "joined", true: "retained"}[keep], func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var active atomic.Bool
			var count atomic.Int64
			var clears atomic.Int64
			var mu sync.Mutex
			sockets := map[net.Conn]bool{}
			accepted := make(chan struct{})
			go func() {
				defer close(accepted)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					mu.Lock()
					sockets[conn] = true
					mu.Unlock()
					if !active.Load() {
						_, _ = conn.Write([]byte("x"))
					}
				}
			}()
			defer func() {
				_ = listener.Close()
				<-accepted
				mu.Lock()
				for conn := range sockets {
					_ = conn.Close()
				}
				mu.Unlock()
			}()
			var retained net.Conn
			defer func() {
				if retained != nil {
					_ = retained.Close()
				}
			}()
			var attempts atomic.Int64
			rule := chaos.Rule{From: "caller", To: "cache", Kind: chaos.KindTCPTimeout, Port: 6379, Percent: 100, Direction: chaos.DirectionBoth}
			control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPut:
					active.Store(true)
					_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "cancel", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
				case http.MethodDelete:
					active.Store(false)
					clears.Add(1)
					w.WriteHeader(204)
				case http.MethodGet:
					_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "cancel", Matches: []api.ScenarioTestChaosMatch{{RuleID: chaos.RuleID(rule), Count: attempts.Load()}}})
				}
			}))
			defer control.Close()
			app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/connections" {
					_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": connectionTestInstance, "active_connections": count.Load()})
					return
				}
				conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond)
				if err != nil {
					w.WriteHeader(503)
					return
				}
				count.Add(1)
				fault := active.Load()
				if fault {
					attempts.Add(1)
				}
				mu.Lock()
				retain := keep && fault && retained == nil
				if retain {
					retained = conn
				}
				mu.Unlock()
				if !retain {
					defer func() { _ = conn.Close(); count.Add(-1) }()
					_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
					buffer := make([]byte, 1)
					_, _ = conn.Read(buffer)
				}
				_, _ = w.Write([]byte(`{"ready":true}`))
			}))
			defer app.Close()
			load := &testLoadSpec{VUs: 2, Rate: 5, Duration: "4s", Thresholds: testLoadThresholds{ErrorRate: func() *float64 { value := 1.0; return &value }()}, ConnectionLeaks: &testConnectionLeaksSpec{Path: "/connections", Within: "300ms", Sustain: "300ms", MaxExtra: 2}, ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "1s"}, {Name: "fault", Duration: "1s", Rules: []testChaosRule{{From: "caller", To: "cache", Kind: "tcp_timeout", Port: 6379, Percent: 100, Direction: "both", MinMatches: 1}}}, {Name: "recovery", Duration: "2s"}}}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			report, _, runErr := runTestScenarioStepLoad(ctx, NewClient(control.URL, "token"), app.URL, "0123456789abcdef0123456789abcdef", nil, testScenarioStep{Name: "load", Requests: []testHTTPRequest{{Name: "probe", Method: "GET", Path: "/dependency", Expect: testHTTPExpect{Status: 200}}}, Load: load}, nil, nil)
			if runErr == nil || report == nil || report.ConnectionLeaks == nil || !report.ConnectionLeaks.Cancelled || report.ConnectionLeaks.PostDrain == nil {
				t.Fatalf("cancellation evidence: %+v %v", report, runErr)
			}
			if active.Load() || clears.Load() == 0 {
				t.Fatal("cancellation left the fault plan active")
			}
			final := report.ConnectionLeaks.PostDrain
			if final.Passed == keep || (!keep && final.Remaining != 0) || (keep && final.Remaining != 1) {
				t.Fatalf("retained socket evidence: %+v", final)
			}
		})
	}
}
