package main

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Zero limits are unlimited. Budgets apply to the combined baseline/current plan.
type testCampaignPlanBudget struct {
	Runs int64 `json:"runs"`
	Deployments int64 `json:"deployments"`
	WorkloadMinutes int64 `json:"workload_minutes"`
}

type testCampaignPlanCosts struct {
	Budget testCampaignPlanBudget `json:"budget"`
	Estimated testCampaignPlanBudget `json:"estimated"`
	Allocation string `json:"allocation"`
}

type testCampaignCostBatch struct {
	ID string `json:"id"`
	Scenario string `json:"scenario"`
	Profile string `json:"profile"`
	Side string `json:"side"`
	Repetitions int `json:"repetitions"`
	Estimated testCampaignPlanBudget `json:"estimated"`
	EntryIndexes []int `json:"entry_indexes"`
	Argv []string `json:"argv"`
}

type testCampaignPlannedCoverage struct {
	BaselineRuns int `json:"baseline_runs"`
	CurrentRuns int `json:"current_runs"`
	BaselineRemaining int `json:"baseline_remaining_runs"`
	CurrentRemaining int `json:"current_remaining_runs"`
	BatchIDs []string `json:"batch_ids"`
}

type testCampaignCostGroup struct {
	scenario, profile, side string
	entries []int
	needed int
	unit testCampaignPlanBudget
}

func testCampaignCostMultiply(a, b int64) (int64, error) {
	if a < 0 || b < 0 || (a != 0 && b > int64(^uint64(0)>>1)/a) {
		return 0, errors.New("campaign cost estimate exceeds supported integer range")
	}
	return a*b, nil
}

func testCampaignCostUnit(scenario testScenario) (testCampaignPlanBudget, error) {
	candidates, err := testCampaignPlanCandidates(scenario)
	if err != nil { return testCampaignPlanBudget{}, err }
	var runs int64
	for _, candidate := range candidates {
		n := int64(candidate.Runs)
		if n > int64(^uint64(0)>>1)-runs { return testCampaignPlanBudget{}, errors.New("campaign run estimate overflow") }
		runs += n
	}
	deployments, err := testCampaignCostMultiply(runs, int64(1+len(scenario.Services)))
	if err != nil { return testCampaignPlanBudget{}, err }
	window := 15*time.Minute
	if scenario.Timeout != "" {
		window, err = time.ParseDuration(scenario.Timeout)
		if err != nil || window <= 0 { return testCampaignPlanBudget{}, errors.New("invalid scenario timeout for cost estimate") }
	}
	// Integer arithmetic rounds up the full workload window, without floating
	// point conversion or duration multiplication overflow.
	whole, err := testCampaignCostMultiply(int64(window/time.Minute), deployments)
	if err != nil { return testCampaignPlanBudget{}, err }
	fraction, err := testCampaignCostMultiply(int64(window%time.Minute), deployments)
	if err != nil { return testCampaignPlanBudget{}, err }
	extra := fraction/int64(time.Minute)
	if fraction%int64(time.Minute) != 0 { extra++ }
	if extra > int64(^uint64(0)>>1)-whole { return testCampaignPlanBudget{}, errors.New("campaign minute estimate overflow") }
	return testCampaignPlanBudget{Runs:runs, Deployments:deployments, WorkloadMinutes:whole+extra}, nil
}

// Groups full-matrix invocations by side/scenario/profile. Allocation is a
// deterministic inexpensive-first heuristic, not a claim of globally optimal coverage.
func optimizeTestCampaignPlan(plan *testCampaignPolicyPlan, scenarios map[string]testScenario, manifest string, budget testCampaignPlanBudget) error {
	if budget.Runs < 0 || budget.Deployments < 0 || budget.WorkloadMinutes < 0 {
		return errors.New("plan budgets must be nonnegative")
	}
	groups := make(map[[3]string]*testCampaignCostGroup)
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		entry.Planned = &testCampaignPlannedCoverage{BaselineRemaining:entry.BaselineAdditional, CurrentRemaining:entry.CurrentAdditional, BatchIDs:[]string{}}
		allowed := make(map[string]bool)
		for _, command := range entry.Suggestions { allowed[command.Side] = true }
		for _, side := range []string{"baseline","current"} {
			if !allowed[side] || entry.RunsPerRepetition <= 0 { continue }
			deficit := entry.BaselineAdditional
			if side == "current" { deficit = entry.CurrentAdditional }
			if deficit <= 0 { continue }
			key := [3]string{entry.Context.Scenario, entry.Context.Profile, side}
			group := groups[key]
			if group == nil {
				scenario, ok := scenarios[key[0]]
				if !ok { return errors.New("planned scenario is missing") }
				unit, err := testCampaignCostUnit(scenario)
				if err != nil { return err }
				group = &testCampaignCostGroup{scenario:key[0], profile:key[1], side:side, unit:unit}
				groups[key] = group
			}
			group.entries = append(group.entries, i)
			group.needed = max(group.needed, (deficit-1)/entry.RunsPerRepetition+1)
		}
		entry.Suggestions = []testCampaignPlanCommand{}
	}
	ordered := make([]*testCampaignCostGroup,0,len(groups))
	for _, group := range groups { ordered = append(ordered, group) }
	sort.Slice(ordered,func(i,j int) bool {
		a,b := ordered[i],ordered[j]
		if a.unit.WorkloadMinutes != b.unit.WorkloadMinutes { return a.unit.WorkloadMinutes < b.unit.WorkloadMinutes }
		if a.unit.Deployments != b.unit.Deployments { return a.unit.Deployments < b.unit.Deployments }
		if a.scenario != b.scenario { return a.scenario < b.scenario }
		if a.profile != b.profile { return a.profile < b.profile }
		return a.side < b.side
	})
	plan.Costs = &testCampaignPlanCosts{Budget:budget, Allocation:"workload-minutes-first, then deployments, scenario, profile, side; shared repetitions use maximum deficit"}
	plan.Batches = []testCampaignCostBatch{}
	var used testCampaignPlanBudget
	for _, group := range ordered {
		// Bound output to twenty CLI batches per shared group.
		count := int64(min(group.needed,400))
		for _, dimension := range []struct{limit,used,unit int64}{
			{budget.Runs,used.Runs,group.unit.Runs},
			{budget.Deployments,used.Deployments,group.unit.Deployments},
			{budget.WorkloadMinutes,used.WorkloadMinutes,group.unit.WorkloadMinutes},
		} {
			limit := int64(^uint64(0)>>1)
			if dimension.limit > 0 { limit = dimension.limit }
			if dimension.unit > 0 { count = min(count,(limit-dimension.used)/dimension.unit) }
		}
		for remaining := count; remaining > 0; {
			repeat := min(int64(20),remaining)
			remaining -= repeat
			id := fmt.Sprintf("coverage-batch-%d",len(plan.Batches)+1)
			cost := testCampaignPlanBudget{Runs:group.unit.Runs*repeat,Deployments:group.unit.Deployments*repeat,WorkloadMinutes:group.unit.WorkloadMinutes*repeat}
			used.Runs += cost.Runs; used.Deployments += cost.Deployments; used.WorkloadMinutes += cost.WorkloadMinutes
			plan.Batches = append(plan.Batches,testCampaignCostBatch{ID:id,Scenario:group.scenario,Profile:group.profile,Side:group.side,
				Repetitions:int(repeat),Estimated:cost,EntryIndexes:append([]int(nil),group.entries...),
				Argv:[]string{"gregale","test","--manifest",manifest,"--scenario",group.scenario,"--engine","real-vm","--profile",group.profile,
					"--repeat",strconv.Itoa(int(repeat)),"--report",id+".json"}})
			for _, index := range group.entries { plan.Entries[index].Planned.BatchIDs = append(plan.Entries[index].Planned.BatchIDs,id) }
		}
		for _, index := range group.entries {
			entry := &plan.Entries[index]
			deficit := entry.BaselineAdditional
			if group.side == "current" { deficit = entry.CurrentAdditional }
			// Saturate at the requested deficit before multiplication.
			covered := deficit
			if count < int64((deficit-1)/entry.RunsPerRepetition+1) { covered = int(count)*entry.RunsPerRepetition }
			if group.side == "current" {
				entry.Planned.CurrentRuns,entry.Planned.CurrentRemaining = covered,deficit-covered
			} else {
				entry.Planned.BaselineRuns,entry.Planned.BaselineRemaining = covered,deficit-covered
			}
		}
	}
	plan.Costs.Estimated = used
	plan.Notes = []string{
		"Execute each top-level batch once; entry batch IDs reference shared commands. No workloads were provisioned.",
		"Each batch executes the complete matrix and campaign repetitions, including cases not selected by requirements.",
		"Estimates assume every workload runs for the scenario timeout (default 15m); workload minutes are conservative planning estimates, not billing.",
		"Budgets apply across both source-revision sides. Allocation is deterministic and inexpensive-first, not globally optimal.",
		"Planned coverage is conditional on successful runtime assertion evidence; it never changes observed coverage counts.",
		"Remaining gaps include budget, environment, declaration and 400-repetition-per-group output limits. Existing execution guards still apply.",
		"Run each side against its intended revision, use fresh report paths, and merge receipts into that side before reevaluating policy.",
	}
	return nil
}
