package platformflags

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261021080640 — automatic ramps advance a rollout stage by stage and
// pause on a breach.

func rampRule(rollout int, onBreach string) Rule {
	return Rule{ID: "rollout", Rollout: intPtr(rollout), Value: boolPtr(true), Ramp: &Ramp{
		Stages: []int{100, 1000, 5000, 10000}, IntervalSeconds: 600, Max5xxBasisPoints: 100, MinRequests: 50, OnBreach: onBreach,
	}}
}

func TestRampStepAdvancesHoldsAndBreaks(t *testing.T) {
	healthy := RampHealth{Known: true, Requests: 1000, Failures: 5} // 50 bp
	cases := []struct {
		name    string
		rule    Rule
		health  RampHealth
		outcome string
		rollout int
		paused  bool
	}{
		{"healthy advances one stage", rampRule(1000, RampPause), healthy, RampAdvance, 5000, false},
		{"unknown health holds", rampRule(1000, RampPause), RampHealth{}, RampHold, 1000, false},
		{"too few requests hold", rampRule(1000, RampPause), RampHealth{Known: true, Requests: 49}, RampHold, 1000, false},
		{"last stage completes", rampRule(10000, RampPause), healthy, RampComplete, 10000, false},
		{"breach pauses in place", rampRule(1000, RampPause), RampHealth{Known: true, Requests: 1000, Failures: 20}, RampPaused, 1000, true},
		{"breach rolls back a stage", rampRule(5000, RampRollback), RampHealth{Known: true, Requests: 1000, Failures: 20}, RampRollBack, 1000, true},
		{"rollback at the first stage only pauses", rampRule(100, RampRollback), RampHealth{Known: true, Requests: 1000, Failures: 20}, RampPaused, 100, true},
	}
	for _, tc := range cases {
		next, outcome, reason := RampStep(tc.rule, tc.health)
		if outcome != tc.outcome || *next.Rollout != tc.rollout || next.Ramp.Paused != tc.paused || reason == "" {
			t.Errorf("%s: outcome=%s rollout=%d paused=%t reason=%q", tc.name, outcome, *next.Rollout, next.Ramp.Paused, reason)
		}
		if *tc.rule.Rollout != tc.rule.Ramp.Stages[indexOf(tc.rule.Ramp.Stages, *tc.rule.Rollout)] || tc.rule.Ramp.Paused {
			t.Errorf("%s: RampStep must not mutate its input", tc.name)
		}
	}
	paused := rampRule(1000, RampPause)
	paused.Ramp.Paused = true
	if _, outcome, _ := RampStep(paused, healthy); outcome != RampHold {
		t.Fatalf("a paused ramp must hold until an operator clears it, got %s", outcome)
	}
}

// TestHealthRoutesAreServedRoutes keeps every health route a pattern apid's
// router registers: the route label of apid_request_total is that pattern.
func TestHealthRoutesAreServedRoutes(t *testing.T) {
	files, err := filepath.Glob("../apid/apidserver/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("apidserver sources: %v", err)
	}
	var sources strings.Builder
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sources.Write(raw)
	}
	for _, flag := range Catalog() {
		for _, route := range flag.HealthRoutes {
			if !strings.Contains(sources.String(), `mux.HandleFunc("`+route+`"`) {
				t.Errorf("%s: health route %q is not registered by apid", flag.Key, route)
			}
		}
	}
}

func indexOf(stages []int, stage int) int {
	for i, s := range stages {
		if s == stage {
			return i
		}
	}
	return 0
}

func TestRampValidation(t *testing.T) {
	valid := Definition{Enabled: true, Rules: []Rule{rampRule(100, RampPause)}}
	raw, _ := json.Marshal(valid)
	if _, err := ParseDefinition(raw); err != nil {
		t.Fatalf("valid ramp refused: %v", err)
	}
	for name, mutate := range map[string]func(*Rule){
		"no rollout":          func(r *Rule) { r.Rollout = nil },
		"rollout off stage":   func(r *Rule) { r.Rollout = intPtr(200) },
		"one stage":           func(r *Rule) { r.Ramp.Stages = []int{100}; r.Rollout = intPtr(100) },
		"descending stages":   func(r *Rule) { r.Ramp.Stages = []int{100, 50} },
		"short interval":      func(r *Rule) { r.Ramp.IntervalSeconds = 299 },
		"unknown on_breach":   func(r *Rule) { r.Ramp.OnBreach = "panic" },
		"negative requests":   func(r *Rule) { r.Ramp.MinRequests = -1 },
		"threshold too large": func(r *Rule) { r.Ramp.Max5xxBasisPoints = 10001 },
		"reason too long":     func(r *Rule) { r.Ramp.PausedReason = strings.Repeat("x", 501) },
	} {
		rule := rampRule(100, RampPause)
		mutate(&rule)
		if err := ValidateDefinition(NormalizeDefinition(Definition{Enabled: true, Rules: []Rule{rule}}), nil); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
	noRoutes := Spec{Key: "x"}
	if err := ValidateRamps(noRoutes, valid); err == nil {
		t.Fatal("a ramp on a flag without health routes must be refused")
	}
	if err := ValidateRamps(Spec{Key: "x", HealthRoutes: []string{"GET /v1/x"}}, valid); err != nil {
		t.Fatalf("ramp with health routes refused: %v", err)
	}
}
