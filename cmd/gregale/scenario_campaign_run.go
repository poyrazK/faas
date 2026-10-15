package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"
)

type testCampaignBatchLauncher func(context.Context, testCampaignCostBatch, string) error

func launchTestCampaignBatch(ctx context.Context, batch testCampaignCostBatch, manifest, directory string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(directory, "batch-"+batch.ID+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	// Never execute saved argv: rebuild only the allowed native test options.
	command := exec.CommandContext(ctx, executable, "test", "--manifest", manifest, "--scenario", batch.Scenario,
		"--engine", "real-vm", "--profile", batch.Profile, "--repeat", fmt.Sprint(batch.Repetitions),
		"--report", filepath.Join(directory, "batch-"+batch.ID+".json"))
	journalDirectory, err := prepareTestCampaignJournal(directory, batch)
	if err != nil {
		return err
	}
	command.Env = append(os.Environ(), testCampaignJournalEnv+"="+journalDirectory)
	command.Stdout, command.Stderr = log, log
	// Let the native runner finish its cleanup after interruption. No forced
	// kill deadline is added; active workers are always drained before return.
	configureTestCampaignWorker(command)
	return command.Run()
}

// Shared by campaign launch and process-boundary acceptance tests.
func configureTestCampaignWorker(command *exec.Cmd) {
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
}

func runTestCampaignBatches(ctx context.Context, plan testCampaignPolicyPlan, scenarios map[string]testScenario,
	directory string, checkpoint *testCampaignCheckpoint, concurrency int,
	guard func(testCampaignCostBatch) error, launch testCampaignBatchLauncher) error {
	if concurrency < 1 || concurrency > 4 {
		return errors.New("campaign concurrency must be 1..4")
	}
	if _, err := os.Lstat(filepath.Join(directory, testCampaignRecoverySealName)); !os.IsNotExist(err) {
		return errors.New("campaign has been reconciled; use its sealed continuation plan")
	}
	if _, err := os.Lstat(filepath.Join(directory, testCampaignHandoffName)); !os.IsNotExist(err) {
		return errors.New("campaign ownership was handed off")
	}
	path := filepath.Join(directory, "checkpoint.json")
	if err := testCampaignCancellationRequested(directory, plan); err != nil {
		checkpoint.Halted = true
		if writeErr := writeTestCampaignJSON(path, checkpoint); writeErr != nil {
			return writeErr
		}
		return err
	}
	allowed := make(map[string]testCampaignCostBatch)
	for _, batch := range plan.Batches {
		if batch.Side == checkpoint.Side {
			allowed[batch.ID] = batch
		}
	}
	for id, saved := range checkpoint.Batches {
		batch, ok := allowed[id]
		if !ok || saved.Status != "done" {
			return errors.New("checkpoint contains uncertain or unknown work; reconcile its evidence before creating a new plan")
		}
		hash, cleanup, err := validateTestCampaignBatchReport(filepath.Join(directory, "batch-"+id+".json"), batch, scenarios[batch.Scenario], checkpoint.Revision)
		if err != nil || hash != saved.ReportSHA256 || cleanup {
			return errors.New("completed checkpoint evidence changed or cleanup is unresolved")
		}
	}
	if checkpoint.Halted {
		return errors.New("campaign is halted; reconcile the recorded failure before making a new plan")
	}
	type result struct {
		batch testCampaignCostBatch
		err   error
	}
	results := make(chan result, concurrency)
	next, active := 0, 0
	var stopped error
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	checkCancellation := func() {
		if stopped != nil {
			return
		}
		if err := testCampaignCancellationRequested(directory, plan); err != nil {
			checkpoint.Halted = true
			stopped = err
			if err := writeTestCampaignJSON(path, checkpoint); err != nil {
				stopped = err
			}
		}
	}
	for next < len(plan.Batches) || active > 0 {
		for stopped == nil && ctx.Err() == nil && active < concurrency && next < len(plan.Batches) {
			checkCancellation()
			if stopped != nil {
				break
			}
			batch := plan.Batches[next]
			next++
			if batch.Side != checkpoint.Side {
				continue
			}
			if _, done := checkpoint.Batches[batch.ID]; done {
				continue
			}
			if err := guard(batch); err != nil {
				stopped = err
				break
			}
			checkCancellation()
			if stopped != nil {
				break
			}
			if _, err := os.Lstat(filepath.Join(directory, "batch-"+batch.ID+".json")); !os.IsNotExist(err) {
				stopped = errors.New("uncheckpointed batch output exists; refusing to overwrite or repeat it")
				break
			}
			if _, err := os.Lstat(filepath.Join(directory, "batch-"+batch.ID+".log")); !os.IsNotExist(err) {
				stopped = errors.New("uncheckpointed batch log exists; reconcile it before repeating execution")
				break
			}
			if _, err := os.Lstat(testCampaignJournalDirectory(directory, batch.ID)); !os.IsNotExist(err) {
				stopped = errors.New("uncheckpointed journal exists; inspect recovery before repeating execution")
				break
			}
			checkpoint.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "running"}
			if err := writeTestCampaignJSON(path, checkpoint); err != nil {
				stopped = err
				break
			}
			active++
			go func(batch testCampaignCostBatch) { results <- result{batch: batch, err: launch(ctx, batch, directory)} }(batch)
		}
		if ctx.Err() != nil && stopped == nil {
			stopped = ctx.Err()
		}
		if active == 0 {
			break
		}
		var completed result
		waiting := true
		for waiting {
			select {
			case completed = <-results:
				waiting = false
			case <-ticker.C:
				checkCancellation()
			}
		}
		active--
		batch := completed.batch
		hash, cleanup, err := validateTestCampaignBatchReport(filepath.Join(directory, "batch-"+batch.ID+".json"), batch, scenarios[batch.Scenario], checkpoint.Revision)
		saved := testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
		if err != nil {
			saved.Status, saved.Reason = "uncertain", "execution did not produce a complete, valid report; inspect batch log and cleanup evidence"
			checkpoint.Halted = true
			if stopped == nil {
				stopped = errors.New(saved.Reason)
			}
		} else if cleanup {
			saved.Reason = "cleanup failed; no further batches may be scheduled"
			checkpoint.Halted = true
			if stopped == nil {
				stopped = errors.New(saved.Reason)
			}
		} else if completed.err != nil && ctx.Err() == nil {
			// Assertion failures normally exit 1. Accept their complete receipts
			// as executed evidence; all other process errors halt scheduling.
			var exit *exec.ExitError
			if !errors.As(completed.err, &exit) || exit.ExitCode() != 1 {
				checkpoint.Halted = true
				saved.Reason = "batch process failed after producing receipts"
				if stopped == nil {
					stopped = errors.New(saved.Reason)
				}
			}
		}
		checkpoint.Batches[batch.ID] = saved
		if err := writeTestCampaignJSON(path, checkpoint); err != nil && stopped == nil {
			stopped = err
		}
	}
	checkCancellation()
	return stopped
}

func cmdTestCampaignRun(args []string) int {
	fs := newFlagSet("test policy run", flag.ContinueOnError)
	handoff := fs.String("handoff", "", "portable import index for parent resolution and ownership")
	planPath := fs.String("plan", "", "saved cost-aware plan JSON")
	policyPath := fs.String("policy", "", "same campaign policy used to plan")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "same scenario manifest used to plan")
	directory := fs.String("directory", "", "private checkpoint/output directory, separate for each side")
	side := fs.String("side", "", "baseline or current")
	revision := fs.String("revision", "", "full clean Git commit for all source workloads on this side")
	concurrency := fs.Int("concurrency", 1, "maximum active batches (1..4)")
	from := fs.String("from", "", "original current report used in plan")
	baseline := fs.String("baseline", "", "original baseline report used in plan")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	decoded, revisionErr := hex.DecodeString(*revision)
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *policyPath == "" || *directory == "" ||
		(*side != "baseline" && *side != "current") || revisionErr != nil || (len(decoded) != 20 && len(decoded) != 32) ||
		*concurrency < 1 || *concurrency > 4 {
		return printErr("Invalid campaign run options", errors.New("--plan, --policy, --directory, --side and full --revision are required; concurrency must be 1..4"))
	}
	resolver, handoffErr := loadTestCampaignBundleResolver(*handoff)
	if handoffErr != nil {
		return printErr("Invalid handoff import", handoffErr)
	}
	if err := resolver.requireActive(); err != nil {
		return printErr("Handoff is not active", err)
	}
	if err := enforceTestCampaignPlanHandoff(*planPath, resolver); err != nil {
		return printErr("Invalid imported plan", err)
	}
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(*planPath, &plan); err != nil {
		return printErr("Invalid campaign plan", err)
	}
	policy, err := readTestCampaignPolicy(*policyPath)
	if err != nil {
		return printErr("Invalid campaign policy", err)
	}
	manifest, manifestDir, err := readTestManifestDocument(*manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		return printErr("Invalid campaign manifest", err)
	}
	if err := validateTestCampaignRunnable(plan, manifest.Scenarios, policy); err != nil {
		return printErr("Cannot execute campaign", err)
	}
	if plan.Recovery != nil && (plan.Recovery.Side != *side || plan.Recovery.Revision != *revision) {
		return printErr("Recovery revision changed", errors.New("side and revision must match the reconciled campaign"))
	}
	if err := validateTestCampaignRecoverySeal(plan, resolver); err != nil {
		return printErr("Invalid recovery seal", err)
	}
	var before, after []testRunReceipt
	for _, input := range []struct {
		path, expected string
		receipts       *[]testRunReceipt
	}{
		{*from, plan.Execution.CurrentSHA256, &after}, {*baseline, plan.Execution.BaselineSHA256, &before},
	} {
		if (input.path == "") != (input.expected == "") {
			return printErr("Campaign input changed", errors.New("supply exactly the original reports used to plan"))
		}
		if input.path != "" {
			var hash string
			*input.receipts, hash, err = readTestBaselineReport(input.path, nil)
			if err != nil || hash != input.expected {
				return printErr("Campaign input changed", errors.New("original report hash differs or is unreadable"))
			}
		}
	}
	absolute, err := filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid output directory", err)
	}
	*directory = absolute
	if err := os.Mkdir(*directory, 0o700); err != nil && !os.IsExist(err) {
		return printErr("Could not create campaign directory", err)
	}
	info, err := os.Lstat(*directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return printErr("Invalid campaign directory", errors.New("directory must be private (0700) and not a symlink"))
	}
	if _, err := os.Lstat(filepath.Join(*directory, testCampaignRecoverySealName)); !os.IsNotExist(err) {
		return printErr("Campaign is reconciled", errors.New("use its sealed continuation plan"))
	}
	if err := checkTestCampaignBundleWork(*directory, resolver, true); err != nil {
		return printErr("Invalid campaign ownership", err)
	}
	releaseHandoff, claimErr := acquireTestCampaignHandoff(resolver)
	if claimErr != nil {
		return printErr("Handoff claim unavailable", claimErr)
	}
	defer releaseHandoff()
	if err := registerTestCampaignHandoffWork(resolver, testCampaignJSONHash(plan), *directory, *side, *revision); err != nil {
		return printErr("Invalid handoff work directory", err)
	}
	if err := registerTestCampaignContinuationOwner(plan, *directory, resolver); err != nil {
		return printErr("Invalid continuation ownership", err)
	}
	lockPath := filepath.Join(*directory, "runner.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return printErr("Campaign directory is locked", err)
	}
	_ = lock.Close()
	defer func() { _ = os.Remove(lockPath) }()
	checkpointPath := filepath.Join(*directory, "checkpoint.json")
	checkpoint := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, Recovery: plan.Recovery, PlanSHA256: testCampaignJSONHash(plan), Side: *side, Revision: *revision, Batches: make(map[string]testCampaignCheckpointBatch)}
	if _, err := os.Lstat(checkpointPath); err == nil {
		if err := readTestCampaignJSON(checkpointPath, &checkpoint); err != nil {
			return printErr("Invalid campaign checkpoint", err)
		}
		if checkpoint.SchemaVersion != 1 || checkpoint.PlanSHA256 != testCampaignJSONHash(plan) ||
			checkpoint.Side != *side || checkpoint.Revision != *revision || checkpoint.Batches == nil || !testCampaignCheckpointLineageMatches(checkpoint, plan) {
			return printErr("Checkpoint does not match", errors.New("plan, revision and side must match the original invocation"))
		}
	} else if !os.IsNotExist(err) {
		return printErr("Could not inspect checkpoint", err)
	} else {
		entries, err := os.ReadDir(*directory)
		if err != nil || len(entries) != 1 {
			return printErr("Campaign directory is not empty", errors.New("use a new directory or a matching checkpoint"))
		}
		if err := writeTestCampaignJSON(checkpointPath, checkpoint); err != nil {
			return printErr("Could not save checkpoint", err)
		}
	}
	if err := bindTestCampaignBundleWork(*directory, resolver); err != nil {
		return printErr("Could not bind imported campaign", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer cancel()
	guard := func(batch testCampaignCostBatch) error {
		if err := checkTestCampaignBundleWork(*directory, resolver, true); err != nil {
			return err
		}
		if err := validateTestCampaignRecoverySeal(plan, resolver); err != nil {
			return err
		}
		currentPolicy, err := readTestCampaignPolicy(*policyPath)
		if err != nil {
			return err
		}
		currentManifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
		if err != nil {
			return err
		}
		if testCampaignJSONHash(currentManifest.Scenarios) != plan.Execution.ManifestSHA256 ||
			testCampaignJSONHash(currentPolicy) != plan.Execution.PolicySHA256 {
			return errors.New("manifest or policy changed while campaign was running")
		}
		return testCampaignCheckRevision(ctx, manifest.Scenarios[batch.Scenario], manifestDir, *revision)
	}
	runErr := runTestCampaignBatches(ctx, plan, manifest.Scenarios, *directory, &checkpoint, *concurrency, guard,
		func(ctx context.Context, b testCampaignCostBatch, dir string) error {
			return launchTestCampaignBatch(ctx, b, *manifestPath, dir)
		})
	paths := []string{}
	original := *from
	if *side == "baseline" {
		original = *baseline
	}
	if original != "" {
		paths = append(paths, original)
	}
	for _, batch := range plan.Batches {
		if batch.Side == *side && checkpoint.Batches[batch.ID].Status == "done" {
			reportPath := filepath.Join(*directory, "batch-"+batch.ID+".json")
			hash, _, err := validateTestCampaignBatchReport(reportPath, batch, manifest.Scenarios[batch.Scenario], *revision)
			if err != nil || hash != checkpoint.Batches[batch.ID].ReportSHA256 {
				return printErr("Checkpoint evidence changed", errors.New("refusing to merge an unverified batch report"))
			}
			paths = append(paths, reportPath)
		}
	}
	if len(paths) > 0 {
		merged := filepath.Join(*directory, "combined-"+*side+".json")
		if err := mergeTestReports(paths, merged); err != nil {
			return printErr("Could not merge campaign evidence", err)
		}
		receipts, _, err := readTestBaselineReport(merged, nil)
		if err != nil {
			return printErr("Could not read merged campaign evidence", err)
		}
		if *side == "baseline" {
			before = receipts
		} else {
			after = receipts
		}
	}
	result, err := evaluateTestCampaignPolicy(policy, before, after, "", "")
	if err != nil {
		return printErr("Could not evaluate campaign policy", err)
	}
	if err := writeTestCampaignJSON(filepath.Join(*directory, "policy-result.json"), result); err != nil {
		return printErr("Could not save campaign policy result", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return printErr("Could not write campaign policy result", err)
	}
	if runErr != nil {
		return printErr("Campaign stopped; evidence and checkpoint retained", runErr)
	}
	if !result.Passed {
		return 2
	}
	return 0
}
