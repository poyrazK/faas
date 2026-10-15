package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func testCampaignPrivateDirectory(path string) error {
	info,err := os.Lstat(path)
	if err != nil { return err }
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("campaign directory must be private (0700) and not a symlink")
	}
	return nil
}

func cmdTestCampaignReconcile(args []string) int {
	fs := newFlagSet("test policy reconcile",flag.ContinueOnError)
	handoff := fs.String("handoff","","portable import index for parent resolution and ownership")
	planPath := fs.String("plan","","original saved campaign plan")
	policyPath := fs.String("policy","","original campaign policy")
	manifestPath := fs.String("manifest","gregale-test.yaml","original scenario manifest")
	directory := fs.String("directory","","interrupted campaign directory")
	output := fs.String("output-directory","","new recovery bundle directory (must not exist)")
	from := fs.String("from","","exact original current report used by the plan")
	baseline := fs.String("baseline","","exact original baseline report used by the plan")
	checkCleanup := fs.Bool("check-cleanup",false,"also inspect known workload sessions through read-only GET requests")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err,flag.ErrHelp) { return 0 }
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *policyPath == "" || *directory == "" || *output == "" {
		return printErr("Invalid reconciliation options",errors.New("--plan, --policy, --directory and a new --output-directory are required"))
	}
	resolver,handoffErr := loadTestCampaignBundleResolver(*handoff)
	if handoffErr != nil { return printErr("Invalid handoff import",handoffErr) }
	if err := resolver.requireActive(); err != nil { return printErr("Handoff is not active",err) }
	if err := enforceTestCampaignPlanHandoff(*planPath,resolver); err != nil { return printErr("Invalid imported plan",err) }
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(*planPath,&plan); err != nil { return printErr("Invalid campaign plan",err) }
	policy,err := readTestCampaignPolicy(*policyPath)
	if err != nil { return printErr("Invalid campaign policy",err) }
	manifest,_,err := readTestManifestDocument(*manifestPath,func(_ string,s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil { return printErr("Invalid campaign manifest",err) }
	if err := validateTestCampaignRunnable(plan,manifest.Scenarios,policy); err != nil { return printErr("Campaign binding changed",err) }
	if err := validateTestCampaignRecoverySeal(plan,resolver); err != nil { return printErr("Invalid parent recovery seal",err) }
	*directory,err = filepath.Abs(*directory)
	if err != nil { return printErr("Invalid campaign directory",err) }
	if err := testCampaignPrivateDirectory(*directory); err != nil { return printErr("Invalid campaign directory",err) }
	if err := checkTestCampaignBundleWork(*directory,resolver,true); err != nil { return printErr("Invalid campaign ownership",err) }
	releaseHandoff,claimErr := acquireTestCampaignHandoff(resolver)
	if claimErr != nil { return printErr("Handoff claim unavailable",claimErr) }
	defer releaseHandoff()
	lockPath := filepath.Join(*directory,"runner.lock")
	lock,err := os.OpenFile(lockPath,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0o600)
	if err != nil { return printErr("Campaign directory is locked",err) }
	_ = lock.Close()
	defer func(){ _ = os.Remove(lockPath) }()
	var checkpoint testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(*directory,"checkpoint.json"),&checkpoint); err != nil { return printErr("Invalid checkpoint",err) }
	revision,revisionErr := hex.DecodeString(checkpoint.Revision)
	if checkpoint.SchemaVersion != 1 || checkpoint.PlanSHA256 != testCampaignJSONHash(plan) || checkpoint.Batches == nil || !testCampaignCheckpointLineageMatches(checkpoint,plan) ||
		(checkpoint.Side != "current" && checkpoint.Side != "baseline") || revisionErr != nil || (len(revision) != 20 && len(revision) != 32) {
		return printErr("Checkpoint binding changed",errors.New("checkpoint does not identify this plan and a revision side"))
	}
	if err := registerTestCampaignHandoffWork(resolver,testCampaignJSONHash(plan),*directory,checkpoint.Side,checkpoint.Revision); err != nil { return printErr("Invalid handoff work directory",err) }
	if plan.Recovery != nil && (plan.Recovery.Side != checkpoint.Side || plan.Recovery.Revision != checkpoint.Revision) {
		return printErr("Recovery revision changed",errors.New("checkpoint does not match its recovery lineage"))
	}
	sealPath := filepath.Join(*directory,testCampaignRecoverySealName)
	if _,err := os.Lstat(sealPath); err == nil {
		var seal testCampaignRecoverySeal
		if err := readTestCampaignJSON(sealPath,&seal); err != nil || !seal.Blocked ||
			seal.ParentPlanSHA256 != checkpoint.PlanSHA256 || seal.CheckpointSHA256 != testCampaignJSONHash(checkpoint) {
			return printErr("Campaign is already reconciled",errors.New("use the existing continuation; only a blocked reconciliation can be refreshed"))
		}
	} else if !os.IsNotExist(err) { return printErr("Could not inspect recovery seal",err) }
	var before,after []testRunReceipt
	for _,input := range []struct{path,hash string; receipts *[]testRunReceipt}{
		{*from,plan.Execution.CurrentSHA256,&after},{*baseline,plan.Execution.BaselineSHA256,&before},
	} {
		if (input.path == "") != (input.hash == "") { return printErr("Original report changed",errors.New("supply exactly the reports used to plan")) }
		if input.path != "" {
			var hash string
			*input.receipts,hash,err = readTestBaselineReport(input.path,nil)
			if err != nil || hash != input.hash { return printErr("Original report changed",errors.New("original report hash differs or is unavailable")) }
		}
	}
	allowed := make(map[string]bool)
	for _,batch := range plan.Batches { if batch.Side == checkpoint.Side { allowed[batch.ID] = true } }
	for id,saved := range checkpoint.Batches {
		if !allowed[id] || (saved.Status != "running" && saved.Status != "uncertain" && saved.Status != "done") {
			return printErr("Invalid checkpoint state",errors.New("checkpoint contains unknown batches or statuses"))
		}
	}
	*output,err = filepath.Abs(*output)
	if err != nil || *output == *directory { return printErr("Invalid recovery directory",errors.New("use a distinct new output directory")) }
	if err := os.Mkdir(*output,0o700); err != nil { return printErr("Could not create recovery directory",err) }
	// A published continuation requires the parent seal written last. A failure
	// before publication leaves inspectable artifacts but no runnable continuation.
	ctx,cancel := context.WithTimeout(context.Background(),30*time.Second)
	defer cancel()
	var reader testCampaignCleanupReader
	if *checkCleanup {
		client,err := authedClient()
		if err != nil { return printErr("Could not authenticate cleanup inspection",err) }
		reader = client
	}
	reconciliation := testCampaignReconciliation{SchemaVersion:1,ParentPlanSHA256:checkpoint.PlanSHA256,
		CheckpointSHA256:testCampaignJSONHash(checkpoint),Side:checkpoint.Side,Revision:checkpoint.Revision,
		Ready:true,Batches:[]testCampaignRecoveryBatch{},Reasons:[]string{}}
	recoveredPaths := []string{}
	for index,batch := range plan.Batches {
		if batch.Side != checkpoint.Side { continue }
		if err := testCampaignAddRecoveryCost(&reconciliation.Allocation,batch.Estimated); err != nil { return printErr("Invalid recovery allocation",err) }
		saved,started := checkpoint.Batches[batch.ID]
		reportPath := filepath.Join(*directory,"batch-"+batch.ID+".json")
		_,reportErr := os.Lstat(reportPath)
		_,logErr := os.Lstat(filepath.Join(*directory,"batch-"+batch.ID+".log"))
		if !started && os.IsNotExist(reportErr) && os.IsNotExist(logErr) {
			reconciliation.Batches = append(reconciliation.Batches,testCampaignRecoveryBatch{ID:batch.ID,Status:"pending",Cleanup:"not_started",Reasons:[]string{}})
			continue
		}
		// Missing or partial evidence never refunds potentially spent work.
		if err := testCampaignAddRecoveryCost(&reconciliation.Reserved,batch.Estimated); err != nil { return printErr("Invalid recovery reservation",err) }
		result,recovered := inspectTestCampaignRecoveryReport(ctx,reportPath,batch,manifest.Scenarios[batch.Scenario],checkpoint,saved,reader)
		if len(recovered) > 0 {
			path := filepath.Join(*output,fmt.Sprintf("recovered-batch-%d.json",index+1))
			if err := writeTestCampaignJSON(path,recovered); err != nil { return printErr("Could not preserve recovered evidence",err) }
			_,result.RecoveredSHA256,err = readTestBaselineReport(path,nil)
			if err != nil { return printErr("Could not verify recovered evidence",err) }
			recoveredPaths = append(recoveredPaths,path)
		}
		if result.Status == "blocked" {
			reconciliation.Ready = false
			for _,reason := range result.Reasons { if len(reconciliation.Reasons) < 100 { reconciliation.Reasons = append(reconciliation.Reasons,batch.ID+": "+reason) } }
		}
		reconciliation.Batches = append(reconciliation.Batches,result)
	}
	reconciliation.Remaining,err = testCampaignRecoveryRemaining(reconciliation.Allocation,reconciliation.Reserved)
	if err != nil { return printErr("Could not reserve recovery budget",err) }
	bindings := &testCampaignExecutionBinding{ManifestSHA256:plan.Execution.ManifestSHA256,PolicySHA256:plan.Execution.PolicySHA256}
	for _,side := range []string{"baseline","current"} {
		paths := []string{}
		original := *from
		if side == "baseline" { original = *baseline }
		if original != "" { paths = append(paths,original) }
		if side == checkpoint.Side { paths = append(paths,recoveredPaths...) }
		if len(paths) == 0 { continue }
		path := filepath.Join(*output,side+".json")
		if err := mergeTestReports(paths,path); err != nil { return printErr("Could not merge recovered evidence",err) }
		receipts,hash,err := readTestBaselineReport(path,nil)
		if err != nil { return printErr("Could not verify recovery report",err) }
		if side == "baseline" { before,bindings.BaselineSHA256 = receipts,hash } else { after,bindings.CurrentSHA256 = receipts,hash }
	}
	continuation,err := buildTestCampaignContinuation(plan,checkpoint,reconciliation,policy,manifest.Scenarios,*manifestPath,*directory,before,after)
	if err != nil { return printErr("Could not plan continuation",err) }
	continuation.Execution = bindings
	if err := validateTestCampaignRunnable(continuation,manifest.Scenarios,policy); err != nil && reconciliation.Ready {
		return printErr("Invalid continuation",err)
	}
	policyResult,err := evaluateTestCampaignPolicy(policy,before,after,"","")
	if err != nil { return printErr("Could not reevaluate recovered coverage",err) }
	for _,artifact := range []struct{name string; value any}{
		{"reconciliation.json",reconciliation},{"continuation-plan.json",continuation},{"policy-result.json",policyResult},
	} {
		if err := writeTestCampaignJSON(filepath.Join(*output,artifact.name),artifact.value); err != nil { return printErr("Could not write recovery bundle",err) }
	}
	seal := testCampaignRecoverySeal{SchemaVersion:1,Blocked:!reconciliation.Ready,ParentPlanSHA256:checkpoint.PlanSHA256,
		CheckpointSHA256:testCampaignJSONHash(checkpoint),ReconciliationSHA256:testCampaignJSONHash(reconciliation),
		ContinuationSHA256:testCampaignJSONHash(continuation)}
	if err := writeTestCampaignJSON(sealPath,seal); err != nil { return printErr("Could not seal reconciled campaign",err) }
	if err := json.NewEncoder(os.Stdout).Encode(reconciliation); err != nil { return printErr("Could not write reconciliation result",err) }
	if !reconciliation.Ready { return 2 }
	return 0
}
