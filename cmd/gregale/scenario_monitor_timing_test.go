package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

type resourceMonitorRunner struct {
	name string
	run  func(context.Context, string, *main.TestLoadPhaseClock) []main.TestConnectionSample
}

func liveResourceMonitors() []resourceMonitorRunner {
	return []resourceMonitorRunner{
		{"connections", func(ctx context.Context, url string, clock *main.TestLoadPhaseClock) []main.TestConnectionSample {
			return main.MonitorTestConnectionLeaks(ctx, url, nil, &main.TestConnectionLeaksSpec{Path: "/observe"}, clock)
		}},
		{"pool", func(ctx context.Context, url string, clock *main.TestLoadPhaseClock) []main.TestConnectionSample {
			var samples []main.TestConnectionSample
			for _, sample := range main.MonitorTestPoolExhaustion(ctx, url, nil, poolTestSpec(), clock) {
				normalized := main.TestConnectionSample{At: sample.At, Phase: sample.Phase, Error: sample.Error}
				if sample.Snapshot != nil {
					normalized.Active, normalized.Instance = sample.Snapshot.Active, sample.Snapshot.Instance
				}
				samples = append(samples, normalized)
			}
			return samples
		}},
	}
}

func writeResourceObservation(w http.ResponseWriter, observer string, active int64) {
	if observer == "pool" {
		snapshot := poolTestSnapshot()
		snapshot.Active = active
		_ = json.NewEncoder(w).Encode(snapshot)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": connectionTestInstance, "active_connections": active})
}

func TestScenarioLiveResourceMonitorsUseCompletedObservations(t *testing.T) {
	for _, observer := range liveResourceMonitors() {
		for _, tc := range []struct {
			name        string
			delayedPoll int32
			within      time.Duration
			pass        bool
		}{
			{"late recovery", 2, 300 * time.Millisecond, false},
			{"healthy delayed response", 1, time.Second, true},
		} {
			t.Run(observer.name+"/"+tc.name, func(t *testing.T) {
				var polls atomic.Int32
				var firstHealthy atomic.Pointer[time.Time]
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					poll := polls.Add(1)
					if poll == tc.delayedPoll {
						time.Sleep(220 * time.Millisecond)
					}
					active := int64(1)
					if poll >= tc.delayedPoll {
						active = 0
						at := time.Now()
						firstHealthy.CompareAndSwap(nil, &at)
					}
					writeResourceObservation(w, observer.name, active)
				}))
				defer server.Close()
				start := time.Now()
				window := main.TestLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(1400 * time.Millisecond)}
				clock := main.NewTestLoadPhaseClock(1)
				(*clock.Windows())[0] = window
				ctx, cancel := context.WithDeadline(t.Context(), window.FinishedAt)
				defer cancel()
				samples := observer.run(ctx, server.URL, clock)
				if len(samples) < 3 || firstHealthy.Load() == nil {
					t.Fatalf("missing observations: %+v", samples)
				}
				for _, sample := range samples {
					if sample.Error != "" {
						t.Fatal(sample.Error)
					}
					if sample.Active == 0 && sample.At.Before(*firstHealthy.Load()) {
						t.Errorf("healthy sample at %v predates its response at %v", sample.At.Sub(start), firstHealthy.Load().Sub(start))
					}
				}
				// Apply a recovery deadline to the observed connection counts. Pool
				// samples use the same timestamp contract as connection samples.
				evidence := main.EvaluateTestConnectionWindow(samples, window, 0, 0, tc.within, 300*time.Millisecond)
				if evidence.Passed != tc.pass {
					t.Fatalf("recovery passed=%t, want %t: %+v", evidence.Passed, tc.pass, evidence)
				}
			})
		}
	}
}

func TestScenarioLiveResourceMonitorsDiscardCrossPhaseResponses(t *testing.T) {
	for _, observer := range liveResourceMonitors() {
		t.Run(observer.name, func(t *testing.T) {
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if polls.Add(1) == 1 {
					time.Sleep(220 * time.Millisecond)
				}
				writeResourceObservation(w, observer.name, 0)
			}))
			defer server.Close()
			start := time.Now()
			clock := main.NewTestLoadPhaseClock(2)
			(*clock.Windows())[0] = main.TestLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(250 * time.Millisecond)}
			(*clock.Windows())[1] = main.TestLoadPhaseWindow{StartedAt: start.Add(250 * time.Millisecond), FinishedAt: start.Add(time.Second)}
			ctx, cancel := context.WithDeadline(t.Context(), start.Add(time.Second))
			defer cancel()
			samples := observer.run(ctx, server.URL, clock)
			if len(samples) == 0 || len(samples) != int(polls.Load())-1 {
				t.Fatalf("response crossing the phase boundary was not discarded: %d samples from %d polls", len(samples), polls.Load())
			}
			for _, sample := range samples {
				if sample.Error != "" || sample.Phase != 1 {
					t.Fatalf("invalid phase attribution: %+v", sample)
				}
			}
		})
	}
}
