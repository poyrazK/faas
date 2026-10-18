// adr: 20261017223000
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestInspectWorkerRecoveryShowsObservationAndAppRetryScope(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name     string
		recovery *api.AppWorkerRecovery
		wants    []string
	}{
		{"unavailable", &api.AppWorkerRecovery{Reason: "state_unavailable"}, []string{"worker recovery: unavailable", "state_unavailable"}},
		{"unobserved", &api.AppWorkerRecovery{Available: true, Status: "not_observed"}, []string{"not_observed", "counts: not observed"}},
		{"capacity", &api.AppWorkerRecovery{Available: true, Eligible: true, Status: "waiting_capacity", Failures: 3, NextAttemptAt: &now,
			Pools: []api.AppWorkerPoolObservation{{Scope: "staging", Desired: 2, Running: 1, ObservedAt: now, Stale: true}}},
			[]string{"worker recovery (app-wide): waiting_capacity", "failures=3", "next attempt:", "worker pool staging: desired=2 running=1", "· stale"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			renderInspectWorkerRecovery(&output, tc.recovery)
			for _, want := range tc.wants {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in %s", want, output.String())
				}
			}
		})
	}
	var old bytes.Buffer
	renderInspectWorkerRecovery(&old, nil)
	if old.Len() != 0 {
		t.Fatalf("older server invented worker evidence: %s", old.String())
	}
}

func TestInspectWatchWorkerRecoveryReportsChangesWithoutClockChurn(t *testing.T) {
	now := time.Now().UTC()
	before := inspectSummary{Operational: &api.AppOperationalSummary{Recovery: api.AppOperationalRecovery{
		Workers: &api.AppWorkerRecovery{Available: true, Eligible: true, Status: "ready", UpdatedAt: &now, NextAttemptAt: &now,
			Pools: []api.AppWorkerPoolObservation{{Scope: "staging", Desired: 1, Running: 1, ObservedAt: now}, {Scope: "default", ObservedAt: now}}}}}}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after inspectSummary
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	after.Operational.Recovery.Workers.UpdatedAt = &later
	after.Operational.Recovery.Workers.NextAttemptAt = &later
	for i := range after.Operational.Recovery.Workers.Pools {
		after.Operational.Recovery.Workers.Pools[i].ObservedAt = later
	}
	changes, err := inspectWatchChanges(&before, after)
	if err != nil || len(changes) != 0 {
		t.Fatalf("observation clocks caused an update: %v %v", changes, err)
	}
	if !before.Operational.Recovery.Workers.Pools[0].ObservedAt.Equal(now) {
		t.Fatal("watch comparison mutated the live observation")
	}
	after.Operational.Recovery.Workers.Pools[0].Running = 0
	changes, err = inspectWatchChanges(&before, after)
	if err != nil || len(changes) == 0 {
		t.Fatalf("missing worker transition hidden: %v %v", changes, err)
	}
}
