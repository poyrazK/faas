package main

import (
	"github.com/onebox-faas/faas/pkg/chaos"
	"testing"
)

func TestScenarioTCPAttemptRecorderSeparatesFaultMatches(t *testing.T) {
	recorder := &scenarioChaosMatchRecorder{pending: make(map[scenarioChaosMatchKey]int64)}
	rule := chaos.Rule{From: "app", To: "database", Kind: chaos.KindTCPTimeout, Port: 5432, Percent: 100}
	recorder.Observe("run", "caller", "generation", rule)
	recorder.ObserveTCPAttempt("run", "caller", "generation", rule)
	recorder.ObserveTCPAttempt("run", "caller", "generation", rule)
	if recorder.pending[scenarioChaosMatchKey{"run", "caller", "generation", chaos.RuleID(rule)}] != 1 || recorder.pending[scenarioChaosMatchKey{"run", "caller", "generation", chaos.TCPAttemptID(rule)}] != 2 {
		t.Fatal(recorder.pending)
	}
	recorder.ObserveTCPAttempt("run", "caller", "next", rule)
	if len(recorder.pending) != 3 {
		t.Fatal("generation counters merged")
	}
}
