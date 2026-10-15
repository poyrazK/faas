package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type testCampaignExecutionBinding struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	PolicySHA256 string `json:"policy_sha256"`
	BaselineSHA256 string `json:"baseline_report_sha256,omitempty"`
	CurrentSHA256 string `json:"current_report_sha256,omitempty"`
}

type testCampaignCheckpoint struct {
	LineageVersion int `json:"lineage_version,omitempty"`
	Recovery *testCampaignRecoveryLink `json:"recovery,omitempty"`
	SchemaVersion int `json:"schema_version"`
	PlanSHA256 string `json:"plan_sha256"`
	Side string `json:"side"`
	Revision string `json:"revision"`
	Halted bool `json:"halted"`
	Batches map[string]testCampaignCheckpointBatch `json:"batches"`
}

type testCampaignCheckpointBatch struct {
	Status string `json:"status"`
	ReportSHA256 string `json:"report_sha256,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func testCampaignJSONHash(value any) string {
	body, err := json.Marshal(value)
	if err != nil { return "" }
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func readTestCampaignJSON(path string, value any) error {
	file, err := openCustomerFile(path)
	if err != nil { return err }
	defer func(){ _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file,8*1024*1024+1))
	if err != nil { return err }
	if len(body) > 8*1024*1024 { return errors.New("campaign document exceeds 8 MiB") }
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil { return err }
	if decoder.Decode(new(any)) != io.EOF { return errors.New("expected exactly one campaign document") }
	return nil
}

func writeTestCampaignJSON(path string, value any) error {
	body, err := json.MarshalIndent(value,"","  ")
	if err != nil { return err }
	return writeTestMergedReport(path,append(body,'\n'))
}

func testCampaignCheckRevision(ctx context.Context, scenario testScenario, manifestDir, revision string) error {
	sources := []string{scenario.Source}
	for _, service := range scenario.Services {
		if service.Fixture == "" { sources = append(sources,service.Source) }
	}
	for _, source := range sources {
		if source == "" { source = "." }
		dir, err := resolveDeploySourceDir(manifestDir,source)
		if err != nil { return err }
		commit, dirty := testWorkloadGitRevision(ctx,dir)
		if commit != revision || dirty == nil || *dirty {
			return errors.New("all source workloads must be at the requested clean Git revision")
		}
		// Untracked, nonignored files can alter the deployed source as well.
		command := exec.CommandContext(ctx,"git","--no-optional-locks","-C",dir,"ls-files","--others","--exclude-standard")
		body, err := command.Output()
		if err != nil || strings.TrimSpace(string(body)) != "" {
			return errors.New("source workload contains untracked files or its state is unavailable")
		}
	}
	return nil
}

func validateTestCampaignRunnable(plan testCampaignPolicyPlan, scenarios map[string]testScenario, policy testCampaignPolicy) error {
	if plan.SchemaVersion != 1 || plan.Costs == nil || plan.Execution == nil ||
		plan.Execution.ManifestSHA256 != testCampaignJSONHash(scenarios) ||
		plan.Execution.PolicySHA256 != testCampaignJSONHash(policy) {
		return errors.New("plan is unbound or manifest/policy changed; generate a new plan")
	}
	seen := make(map[string]bool)
	var total testCampaignPlanBudget
	for _, batch := range plan.Batches {
		if batch.ID == "" || filepath.Base(batch.ID) != batch.ID || strings.ContainsAny(batch.ID,"/\\") || batch.ID == "." || batch.ID == ".." || seen[batch.ID] {
			return errors.New("invalid or duplicate campaign batch ID")
		}
		seen[batch.ID] = true
		if batch.Side != "baseline" && batch.Side != "current" { return errors.New("invalid batch side") }
		if batch.Repetitions < 1 || batch.Repetitions > 20 { return errors.New("batch repeat must be 1..20") }
		if batch.Profile != "warm" && batch.Profile != "cold" && batch.Profile != "restored" { return errors.New("invalid native profile") }
		scenario, ok := scenarios[batch.Scenario]
		if !ok { return errors.New("batch scenario missing from manifest") }
		unit, err := testCampaignCostUnit(scenario)
		if err != nil { return err }
		cost := testCampaignPlanBudget{}
		for _, dimension := range []struct{unit int64; target *int64}{
			{unit.Runs,&cost.Runs},{unit.Deployments,&cost.Deployments},{unit.WorkloadMinutes,&cost.WorkloadMinutes},
		} {
			n, err := testCampaignCostMultiply(dimension.unit,int64(batch.Repetitions))
			if err != nil { return err }
			*dimension.target = n
		}
		if cost != batch.Estimated { return errors.New("batch cost changed; replan") }
		for _, dimension := range []struct{n int64; sum *int64}{
			{cost.Runs,&total.Runs},{cost.Deployments,&total.Deployments},{cost.WorkloadMinutes,&total.WorkloadMinutes},
		} {
			if dimension.n > int64(^uint64(0)>>1)-*dimension.sum { return errors.New("campaign total cost overflow") }
			*dimension.sum += dimension.n
		}
	}
	if total != plan.Costs.Estimated { return errors.New("campaign total cost does not match batches") }
	for _, dimension := range []struct{limit,used int64}{
		{plan.Costs.Budget.Runs,total.Runs},{plan.Costs.Budget.Deployments,total.Deployments},
		{plan.Costs.Budget.WorkloadMinutes,total.WorkloadMinutes},
	} {
		if dimension.limit < 0 || (dimension.limit > 0 && dimension.used > dimension.limit) {
			return errors.New("saved campaign exceeds its budget")
		}
	}
	if link := plan.Recovery; link != nil {
		remaining,err := testCampaignRecoveryRemaining(link.Allocation,link.Reserved)
		if err != nil || link.Blocked || remaining != link.Remaining || plan.Costs.Budget != remaining ||
			total.Runs > remaining.Runs || total.Deployments > remaining.Deployments || total.WorkloadMinutes > remaining.WorkloadMinutes {
			return errors.New("recovery plan exceeds its remaining side allocation or is blocked")
		}
		for _,batch := range plan.Batches { if batch.Side != link.Side { return errors.New("recovery cannot schedule the other revision side") } }
	}
	return nil
}

func testCampaignReceiptCleanupFailed(r testRunReceipt, depth int) bool {
	if depth > 32 || r.CleanupError != "" { return true }
	if r.FaultMatrix != nil {
		for _, child := range r.FaultMatrix.Cases { if testCampaignReceiptCleanupFailed(child,depth+1) { return true } }
	}
	if r.Resilience != nil {
		for _, child := range r.Resilience.History { if testCampaignReceiptCleanupFailed(child,depth+1) { return true } }
	}
	return false
}

func validateTestCampaignBatchReport(path string, batch testCampaignCostBatch, scenario testScenario, revision string) (string,bool,error) {
	roots, hash, err := readTestBaselineReport(path,nil)
	if err != nil { return "",false,err }
	cleanupFailed := false
	state := testReportMergeState{SourceIdentities:make(map[testReportSourceContext]string),RunIDs:make(map[string]string)}
	var leaves []testRunReceipt
	for i, root := range roots {
		if err := state.checkReceipt(root,fmt.Sprint(i),0); err != nil { return "",false,err }
		cleanupFailed = cleanupFailed || testCampaignReceiptCleanupFailed(root,0)
		collectTestFailureLeaves(root,"",&leaves)
	}
	candidates, err := testCampaignPlanCandidates(scenario)
	if err != nil { return "",cleanupFailed,err }
	expected := make(map[[2]string]int)
	for _, candidate := range candidates { expected[[2]string{candidate.Case,candidate.Hash}] += candidate.Runs*batch.Repetitions }
	for _, leaf := range leaves {
		if leaf.Scenario != batch.Scenario || leaf.Profile != batch.Profile || leaf.Engine != "real-vm" {
			return "",cleanupFailed,errors.New("batch report has another execution context")
		}
		key := [2]string{leaf.Case,testDigestScopeFingerprint([]testRunReceipt{leaf})}
		// Skipped runs never count as completed work: retain their evidence but
		// stop the campaign rather than scheduling automatic replacements.
		if leaf.Status == "skipped" || expected[key] <= 0 { return "",cleanupFailed,errors.New("batch report is skipped, unexpected or incomplete") }
		expected[key]--
		if leaf.SourceRevision == nil { return "",cleanupFailed,errors.New("batch source revision unavailable") }
		names := []string{scenario.Project}
		for name, service := range scenario.Services { if service.Fixture == "" { names = append(names,name) } }
		for _, name := range names {
			source, ok := leaf.SourceRevision.Workloads[name]
			if !ok || source.GitCommit != revision || source.TrackedChanges == nil || *source.TrackedChanges {
				return "",cleanupFailed,errors.New("batch source revision differs from requested revision")
			}
		}
	}
	for _, remaining := range expected { if remaining != 0 { return "",cleanupFailed,errors.New("batch report lacks planned executions") } }
	return hash,cleanupFailed,nil
}
