package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/actioncompare"
)

func testColdStartSpec(s testScenario, profile string) *rehearsalcold.Spec {
	if s.CommandExecution == nil || profile != "cold" {
		return nil
	}
	within := 2 * time.Minute
	if s.CommandExecution.StartupWithin != "" {
		within, _ = time.ParseDuration(s.CommandExecution.StartupWithin)
	}
	return &rehearsalcold.Spec{Version: 1, WithinMS: within.Milliseconds()}
}
func testColdStartDigest(s testScenario, profile string, names []string) string {
	spec := testColdStartSpec(s, profile)
	if spec == nil {
		return ""
	}
	_, d, _ := rehearsalcold.Normalize(*spec, names)
	return d
}
func verifyTestContainedColdWakes(ctx context.Context, client testServiceWakeClient, slugs map[string]string, baselines map[string]map[string]bool, r *testRunReceipt) {
	r.ColdWake = map[string]testServiceWakeEvidence{}
	for _, name := range sortedTestColdNames(slugs) {
		e, err := verifyTestServiceWake(ctx, client, slugs[name], "cold", baselines[name])
		if e.WakeID != "" {
			r.ColdWake[name] = e
		}
		if err != nil {
			if r.Error != "" {
				r.Error += "; "
			}
			r.Error += fmt.Sprintf("cold-start %s lifecycle evidence: %v", name, err)
			r.Status = "failed"
		}
	}
}
func sortedTestColdNames(slugs map[string]string) []string {
	names := make([]string, 0, len(slugs))
	for n := range slugs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func validateTestColdBindings(r testRunReceipt, e *rehearsalcold.Evidence) error {
	if e == nil {
		return nil
	}
	if e.RunID != r.RunID {
		return errors.New("cold-start run identity differs")
	}
	for _, p := range e.Preparations {
		wake, found := r.ColdWake[p.Workload]
		if r.WorkloadAppIDs[p.Workload] != p.AppID || r.WorkloadDeploymentIDs[p.Workload] != p.DeploymentID || !found || wake.WakeID != p.WakeID || wake.Method != p.Method {
			return errors.New("cold-start execution or wake identity differs from this run")
		}
	}
	return nil
}

func testColdStartBudgetError(e *rehearsalcold.Evidence) string {
	if e == nil {
		return ""
	}
	var failures []string
	for _, p := range e.Preparations {
		if p.StartupMS > e.Spec.WithinMS {
			failures = append(failures, fmt.Sprintf("cold-start %s startup %dms exceeded %dms", p.Workload, p.StartupMS, e.Spec.WithinMS))
		}
	}
	sort.Strings(failures)
	return strings.Join(failures, "; ")
}

// A measured budget violation does not erase complete operation evidence.
// Other execution errors and cleanup failures keep the normal incomplete rule.
func testColdStartBudgetOnlyFailure(r testRunReceipt) bool {
	if r.Status != "failed" || r.Actions == nil || r.Actions.Network != "contained" || r.Actions.Status != "applied" || r.Actions.Evidence == nil {
		return false
	}
	e := r.Actions.Evidence
	s := e.Snapshot
	if e.Status != "complete" || e.Error != "" || s == nil || s.ColdStart == nil || r.Actions.ColdStartSHA256 != s.ColdStart.PlanSHA256 {
		return false
	}
	want := testColdStartBudgetError(s.ColdStart)
	if want == "" || r.Error != want || validateTestColdBindings(r, s.ColdStart) != nil {
		return false
	}
	_, err := actioncompare.ValidateSnapshot(s)
	return err == nil
}
