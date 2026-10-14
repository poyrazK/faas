package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestRunTestChaosScheduleChangesPlansAndCapturesPerPhaseMatches(t *testing.T) {
	var mu sync.Mutex
	var generation, ruleID string
	var nextGeneration int
	var events []string
	var clock *testLoadPhaseClock
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			var request api.InjectScenarioTestChaosRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			nextGeneration++
			if clock == nil || clock.phaseAt(time.Now()) != nextGeneration {
				t.Errorf("install generation %d observed phase %d; want the phase boundary before the API mutation", nextGeneration, clock.phaseAt(time.Now()))
			}
			generation = "generation-" + string(rune('0'+nextGeneration))
			if len(request.Rules) != 1 {
				http.Error(w, "expected one rule", http.StatusBadRequest)
				return
			}
			rule := request.Rules[0]
			ruleID = chaos.RuleID(chaos.Rule{From: rule.From, To: rule.To, Kind: rule.Kind, Percent: rule.Percent, LatencyMS: rule.LatencyMS, StatusCode: rule.StatusCode, Seed: rule.Seed})
			events = append(events, "install")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{
				ExpiresAt: time.Now().Add(time.Minute), RulesInstalled: len(request.Rules), Generation: generation,
			})
		case http.MethodDelete:
			if clock == nil || clock.phaseAt(time.Now()) != 1 {
				t.Errorf("clear observed phase %d; want the fault phase until the API clears it", clock.phaseAt(time.Now()))
			}
			events = append(events, "clear")
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			events = append(events, "matches")
			response := api.ScenarioTestChaosMatchesResponse{Generation: generation}
			if ruleID != "" {
				response.Matches = []api.ScenarioTestChaosMatch{{RuleID: ruleID, Count: 1}}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(response)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	fault := testChaosRule{To: "cache", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503, MinMatches: 1}
	phases := []testLoadChaosPhase{
		{Name: "baseline", Duration: time.Second},
		{Name: "fault", Duration: time.Second, Rules: []testChaosRule{fault}},
		{Name: "recovery", Duration: time.Second},
	}
	clock = newTestLoadPhaseClock(len(phases))
	started := time.Now()
	result, err := runTestChaosSchedule(context.Background(), NewClient(server.URL, "test-token"), "0123456789abcdef0123456789abcdef", phases, clock, started)
	if err != nil {
		t.Fatalf("run chaos schedule: %v", err)
	}
	if len(result.Phases) != len(phases) || result.Phases[0] != nil || result.Phases[1] == nil || result.Phases[2] != nil {
		t.Fatalf("phase evidence = %+v", result.Phases)
	}
	if len(result.Phases[1].Matches) != 1 || result.Phases[1].Matches[0].Matches != 1 {
		t.Fatalf("fault phase matches = %+v", result.Phases[1].Matches)
	}
	if window := clock.window(2); window.StartedAt.IsZero() || window.FinishedAt.IsZero() {
		t.Fatalf("recovery phase window = %+v", window)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 4 || events[0] != "install" || events[1] != "clear" || events[2] != "matches" {
		t.Fatalf("chaos API event order = %v", events)
	}
}
