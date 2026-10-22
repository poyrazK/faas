// adr: 20261017223000
package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
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
			main.RenderInspectWorkerRecovery(&output, tc.recovery)
			for _, want := range tc.wants {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q in %s", want, output.String())
				}
			}
		})
	}
	var old bytes.Buffer
	main.RenderInspectWorkerRecovery(&old, nil)
	if old.Len() != 0 {
		t.Fatalf("older server invented worker evidence: %s", old.String())
	}
}

func TestInspectWatchWorkerRecoveryReportsChangesWithoutClockChurn(t *testing.T) {
	now := time.Now().UTC()
	before := main.InspectSummary{Operational: &api.AppOperationalSummary{Recovery: api.AppOperationalRecovery{
		Workers: &api.AppWorkerRecovery{Available: true, Eligible: true, Status: "ready", UpdatedAt: &now, NextAttemptAt: &now,
			Pools: []api.AppWorkerPoolObservation{{Scope: "staging", Desired: 1, Running: 1, ObservedAt: now}, {Scope: "default", ObservedAt: now}}}}}}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after main.InspectSummary
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	after.Operational.Recovery.Workers.UpdatedAt = &later
	after.Operational.Recovery.Workers.NextAttemptAt = &later
	for i := range after.Operational.Recovery.Workers.Pools {
		after.Operational.Recovery.Workers.Pools[i].ObservedAt = later
	}
	changes, err := main.InspectWatchChanges(&before, after)
	if err != nil || len(changes) != 0 {
		t.Fatalf("observation clocks caused an update: %v %v", changes, err)
	}
	if !before.Operational.Recovery.Workers.Pools[0].ObservedAt.Equal(now) {
		t.Fatal("watch comparison mutated the live observation")
	}
	after.Operational.Recovery.Workers.Pools[0].Running = 0
	changes, err = main.InspectWatchChanges(&before, after)
	if err != nil || len(changes) == 0 {
		t.Fatalf("missing worker transition hidden: %v %v", changes, err)
	}
}

// adr: 20261018090000
func TestInspectWorkerAdmissionBlockersShowLimitsCooldownAndStaleness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	used, limit, requested := int64(0), int64(3), int64(1000)
	retry := now.Add(time.Minute)
	recovery := &api.AppWorkerRecovery{Available: true, Scope: "app", Status: "waiting_capacity",
		Pools: []api.AppWorkerPoolObservation{{Scope: "staging", Desired: 2, ObservedAt: now, Blockers: []api.WorkerAdmissionBlocker{
			{Code: "account_worker_limit", ObservedAt: now, Resource: "replicas", Used: &used, Limit: &limit},
			{Code: "node_cpu", ObservedAt: now.Add(-time.Hour), Stale: true, Resource: "cpu_millicores", Requested: &requested},
			{Code: "retry_cooldown", ObservedAt: now, RetryAt: &retry},
		}}}}
	var output bytes.Buffer
	main.RenderInspectWorkerRecovery(&output, recovery)
	for _, want := range []string{"admission blocker: account_worker_limit", "used=0 limit=3", "resource=cpu_millicores requested=1000",
		"· stale", "retry after " + retry.Format(time.RFC3339), "draining workers hold slots", "app-wide retry deadline"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output.String())
		}
	}
	recovery.Pools[0].Blockers = nil
	output.Reset()
	main.RenderInspectWorkerRecovery(&output, recovery)
	if !strings.Contains(output.String(), "admission blockers: not observed") {
		t.Fatalf("old response invented a cause: %s", output.String())
	}
}

// adr: 20261018090000
func TestInspectWatchWorkerAdmissionRetainsMeaningfulChanges(t *testing.T) {
	now := time.Now().UTC()
	used, limit := int64(3), int64(3)
	retry := now.Add(time.Minute)
	before := main.InspectSummary{Operational: &api.AppOperationalSummary{Recovery: api.AppOperationalRecovery{Workers: &api.AppWorkerRecovery{
		Available: true, Failures: 1, Status: "waiting_capacity", Pools: []api.AppWorkerPoolObservation{{Scope: "default", Desired: 1,
			Blockers: []api.WorkerAdmissionBlocker{
				{Code: "account_worker_limit", ObservedAt: now, Resource: "replicas", Used: &used, Limit: &limit},
				{Code: "retry_cooldown", ObservedAt: now, RetryAt: &retry},
			}}}}}}}
	encoded, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*api.AppWorkerPoolObservation)
		want   bool
	}{
		{"clocks", func(p *api.AppWorkerPoolObservation) {
			for i := range p.Blockers {
				p.Blockers[i].ObservedAt = now.Add(time.Second)
			}
		}, false},
		{"order", func(p *api.AppWorkerPoolObservation) { p.Blockers[0], p.Blockers[1] = p.Blockers[1], p.Blockers[0] }, false},
		{"usage", func(p *api.AppWorkerPoolObservation) { *p.Blockers[0].Used = 2 }, true},
		{"reason", func(p *api.AppWorkerPoolObservation) { p.Blockers[0].Code = "concurrency_limit" }, true},
		{"staleness", func(p *api.AppWorkerPoolObservation) { p.Blockers[0].Stale = true }, true},
		{"deadline", func(p *api.AppWorkerPoolObservation) { later := retry.Add(time.Second); p.Blockers[1].RetryAt = &later }, true},
		{"clear", func(p *api.AppWorkerPoolObservation) { p.Blockers = nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var after main.InspectSummary
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatal(err)
			}
			tc.change(&after.Operational.Recovery.Workers.Pools[0])
			changes, err := main.InspectWatchChanges(&before, after)
			if err != nil || (len(changes) > 0) != tc.want {
				t.Fatalf("changes=%v want=%v err=%v", changes, tc.want, err)
			}
		})
	}
	if !before.Operational.Recovery.Workers.Pools[0].Blockers[0].ObservedAt.Equal(now) || used != 3 {
		t.Fatal("comparison mutated source evidence")
	}
}
