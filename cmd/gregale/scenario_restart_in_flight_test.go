package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func restartFlightSpec() *testRestartInFlightSpec {
	return &testRestartInFlightSpec{
		Request:     testHTTPRequest{Name: "write", Method: "POST", Path: "/write", JSON: map[string]any{"secret": "private-payload"}, Expect: testHTTPExpect{Status: 200}},
		Checkpoints: []testRestartCheckpointSpec{{Workload: "app", Request: testHTTPRequest{Name: "checkpoint", Method: "GET", Path: "/checkpoint", Expect: testHTTPExpect{Status: 200, JSON: map[string]any{"/pending": true}}}}},
	}
}
func TestScenarioRestartInFlightLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "service", "missing-service-url", "early", "checkpoint", "pre-storage", "request-error", "readiness", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var active atomic.Bool
			var replaced atomic.Bool
			release := make(chan struct{})
			row := restartStatusTestReceipt("completed")
			fake := &scenarioRestartFake{row: row, requestID: row.WakeID}
			fake.onRestart = func() {
				if !active.Load() {
					t.Error("restart submitted without pending request")
				}
				replaced.Store(true)
			}
			if mode == "request-error" {
				fake.err = errors.New("unavailable")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/write":
					_, _ = io.Copy(io.Discard, r.Body)
					_ = r.Body.Close()
					active.Store(true)
					defer active.Store(false)
					if mode != "early" {
						select {
						case <-r.Context().Done():
							return
						case <-release:
						}
					}
					w.WriteHeader(503)
				case "/checkpoint":
					if mode == "early" {
						w.WriteHeader(503)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]bool{"pending": active.Load() && mode != "checkpoint"})
				case "/ready":
					if !replaced.Load() || mode == "readiness" {
						w.WriteHeader(503)
					}
				case "/ledger":
					id := "private-ledger-value"
					if mode == "pre-storage" {
						id = "missing"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
				}
			}))
			defer server.Close()
			spec := restartScenarioSpec()
			spec.InFlight = restartFlightSpec()
			env := []string{"GREGALE_TEST_APP_SLUG=app"}
			if mode == "service" {
				service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/write" {
						t.Error("background write targeted the service instead of primary app")
						w.WriteHeader(500)
						return
					}
					server.Config.Handler.ServeHTTP(w, r)
				}))
				defer service.Close()
				spec.Workload = "payment"
				env = append(env, "GREGALE_TEST_SERVICE_PAYMENT_APP_SLUG=payment", "GREGALE_TEST_SERVICE_PAYMENT_URL="+service.URL)
				probe := spec.InFlight.Checkpoints[0]
				probe.Workload, probe.Request.Name = "payment", "service-checkpoint"
				spec.InFlight.Checkpoints = append(spec.InFlight.Checkpoints, probe)
			}
			if mode == "missing-service-url" {
				spec.InFlight.Checkpoints[0].Workload = "payment"
			}

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			evidence, err := runTestRestart(ctx, fake, server.URL, env, nil, "run", spec, map[string]string{"steps.seed.id": "private-ledger-value"})
			success := mode == "success" || mode == "service"
			if (err == nil) != success {
				t.Fatalf("evidence %+v, error %v", evidence, err)
			}
			flight := evidence.InFlight
			if flight == nil || !flight.Joined {
				t.Fatalf("request not joined: %+v", evidence)
			}
			if mode == "early" && flight.ResponseStatus != 503 {
				t.Fatalf("early response lost: %+v", flight)
			}
			if success {
				if !flight.PendingAtRestart || !flight.RestartCompleted || fake.requests != 1 {
					t.Fatalf("missing replacement proof: %+v", flight)
				}
				if mode == "success" && flight.Outcome != "canceled_after_restart" {
					t.Fatalf("unexpected outcome %+v", flight)
				}
			} else if fake.requests > 1 || (mode == "early" || mode == "checkpoint" || mode == "cancel" || mode == "pre-storage" || mode == "missing-service-url") && fake.requests != 0 {
				t.Fatal("restart despite failed checkpoint or repeated submission")
			}
			encoded, _ := json.Marshal(evidence)
			if strings.Contains(string(encoded), "private-payload") || strings.Contains(string(encoded), "private-ledger-value") {
				t.Fatal("private data exposed")
			}
		})
	}
}

// Releasing the server handler does not synchronize completion of the HTTP
// client goroutine. Publish its completed result before testing the ended path.
func TestScenarioRestartInFlightCompletedResponseIsPreserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	evidence := &testRestartInFlightEvidence{RestartCompleted: true}
	flight := &testRestartFlight{cancel: cancel, done: make(chan testHTTPRequestEvidence, 1), evidence: evidence}
	flight.done <- testHTTPRequestEvidence{Name: "write", Status: 503}
	flight.close()
	if !evidence.Joined || evidence.Outcome != "request_ended" || evidence.ResponseStatus != 503 || ctx.Err() != context.Canceled {
		t.Fatalf("completed response not preserved: %+v", evidence)
	}
	flight.close()
	if evidence.Outcome != "request_ended" || evidence.ResponseStatus != 503 {
		t.Fatalf("repeated close changed completed evidence: %+v", evidence)
	}
}

func TestScenarioRestartInFlightValidation(t *testing.T) {
	for _, mode := range []string{"valid", "no-checkpoint", "mutation", "capture", "unknown-workload", "service-auth", "future-reference", "duplicate", "no-recovery"} {
		t.Run(mode, func(t *testing.T) {
			spec := restartScenarioSpec()
			spec.InFlight = restartFlightSpec()
			scenario := testScenario{Timeout: "5m", Project: "demo", Steps: []testScenarioStep{
				{Name: "baseline", Requests: []testHTTPRequest{{Name: "seed", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200}, Capture: map[string]string{"id": "/id"}}}},
				{Name: "replace", Restart: &spec},
				{Name: "recovery", Requests: []testHTTPRequest{{Name: "future", Method: "GET", Path: "/ledger", Expect: testHTTPExpect{Status: 200}, Capture: map[string]string{"future_id": "/id"}}}},
			}}
			switch mode {
			case "no-checkpoint":
				spec.InFlight.Checkpoints = nil
			case "mutation":
				spec.InFlight.Checkpoints[0].Request.Method = "POST"
			case "capture":
				spec.InFlight.Request.Capture = map[string]string{"id": "/id"}
			case "unknown-workload":
				spec.InFlight.Checkpoints[0].Workload = "missing"
			case "service-auth":
				scenario.Services = map[string]testService{"payment": {}}
				scenario.Consumers = []testConsumer{{Name: "customer"}}
				spec.InFlight.Checkpoints[0].Workload = "payment"
				spec.InFlight.Checkpoints[0].Request.As = "customer"
			case "future-reference":
				spec.InFlight.Request.JSON = map[string]any{"id": "${steps.future.future_id}"}
			case "duplicate":
				spec.InFlight.Request.Name = "seed"
			case "no-recovery":
				scenario.Steps = append(scenario.Steps[:1], scenario.Steps[2], scenario.Steps[1])
			}
			err := validateTestScenarioSteps(scenario, nil)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestHTTPInFlightExampleManifest(t *testing.T) {
	manifest, dir, err := readTestManifestDocument("../../examples/scenario-http-write/gregale-in-flight-test.yaml", func(string, testScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collectTestValidation(manifest.Scenarios, dir, "replace-during-write"); err != nil {
		t.Fatal(err)
	}
}
