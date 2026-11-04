package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func TestScenarioPostDrainUsesCompletedObservations(t *testing.T) {
	for _, observer := range []struct {
		name string
		run  func(context.Context, string, string) (bool, string)
	}{
		{"pool", func(ctx context.Context, url, within string) (bool, string) {
			spec := poolTestSpec()
			spec.Within, spec.Sustain = within, "300ms"
			baseline := poolTestSnapshot()
			result := main.CheckTestPoolAfterDrain(ctx, url, nil, spec, baseline, baseline)
			return result.Passed, result.Error
		}},
		{"connections", func(ctx context.Context, url, within string) (bool, string) {
			spec := &main.TestConnectionLeaksSpec{Path: "/connections", Within: within, Sustain: "300ms"}
			result := main.CheckTestConnectionsAfterDrain(ctx, url, nil, spec, 0, connectionTestInstance)
			return result.Passed, result.Error
		}},
	} {
		for _, tc := range []struct {
			name, within string
			delayedPoll  int32
			pass         bool
		}{
			{"late recovery", "300ms", 2, false},
			{"full sustain after response", "1s", 1, true},
		} {
			t.Run(observer.name+"/"+tc.name, func(t *testing.T) {
				var polls atomic.Int32
				var firstHealthy atomic.Pointer[time.Time]
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					poll := polls.Add(1)
					if poll == tc.delayedPoll {
						// Stay below the 250ms HTTP timeout. With 100ms between
						// polls, the second response misses the 300ms deadline.
						time.Sleep(220 * time.Millisecond)
					}
					active := int64(0)
					if poll < tc.delayedPoll {
						active = 1
					} else {
						at := time.Now()
						firstHealthy.CompareAndSwap(nil, &at)
					}
					if observer.name == "pool" {
						snapshot := poolTestSnapshot()
						snapshot.Active = active
						_ = json.NewEncoder(w).Encode(snapshot)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": connectionTestInstance, "active_connections": active})
					}
				}))
				defer server.Close()
				passed, problem := observer.run(t.Context(), server.URL, tc.within)
				if passed != tc.pass {
					t.Fatalf("post-drain passed=%t, want %t: %s", passed, tc.pass, problem)
				}
				if !tc.pass && !strings.Contains(problem, "within the deadline") {
					t.Fatalf("missing late recovery failure: %s", problem)
				}
				if tc.pass {
					at := firstHealthy.Load()
					if at == nil || time.Since(*at) < 300*time.Millisecond {
						t.Fatal("passed before a full 300ms of observed health")
					}
				}
			})
		}
	}
}
