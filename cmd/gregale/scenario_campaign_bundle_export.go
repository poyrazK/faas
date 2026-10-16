package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

func collectTestCampaignBundle(plan testCampaignPolicyPlan, checkpoint testCampaignCheckpoint, policy testCampaignPolicy,
	scenarios map[string]testScenario, directory, from, baseline, recovery string, resolver *testCampaignBundleResolver) (testCampaignBundle, error) {
	bundle := testCampaignBundle{SchemaVersion: 1, SourceFrozen: true, PlanSHA256: testCampaignJSONHash(plan), Side: checkpoint.Side,
		Revision: checkpoint.Revision, HistoryComplete: true, Nodes: []testCampaignBundleNode{}, Files: []testCampaignBundleFile{}}
	known := []string{}
	for _, scenario := range scenarios {
		for _, value := range scenario.Secrets {
			known = append(known, value)
		}
		for _, service := range scenario.Services {
			for _, value := range service.Secrets {
				known = append(known, value)
			}
		}
	}
	var total int64
	add := func(name string, body []byte) error {
		total += int64(len(body))
		if total > testCampaignBundleRawLimit {
			return errors.New("bundle exceeds 64 MiB of decoded artifacts")
		}
		if err := checkTestCampaignBundleSensitive(body, known); err != nil {
			return err
		}
		bundle.Files = append(bundle.Files, testCampaignBundleFile{Path: name, SHA256: testCampaignBundleHash(body), Data: body})
		return nil
	}
	addFile := func(name, source string, optional bool) error {
		body, err := readTestCampaignBundleBytes(source, testCampaignBundleRawLimit)
		if optional && os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return add(name, body)
	}
	planBody, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return bundle, err
	}
	if err := add("plan.json", planBody); err != nil {
		return bundle, err
	}
	policyBody, err := yaml.Marshal(policy)
	if err != nil {
		return bundle, err
	}
	if err := add("policy.yaml", policyBody); err != nil {
		return bundle, err
	}
	for _, input := range []struct{ name, source, hash string }{{"current.json", from, plan.Execution.CurrentSHA256}, {"baseline.json", baseline, plan.Execution.BaselineSHA256}} {
		if (input.source == "") != (input.hash == "") {
			return bundle, errors.New("supply exactly the original report inputs")
		}
		if input.source != "" {
			body, err := readTestCampaignBundleBytes(input.source, testCampaignBundleRawLimit)
			if err != nil || testCampaignBundleHash(body) != input.hash {
				return bundle, errors.New("original report is missing or changed")
			}
			if err := add(input.name, body); err != nil {
				return bundle, err
			}
		}
	}
	for _, batch := range plan.Batches {
		if batch.Side != checkpoint.Side {
			continue
		}
		if _, recorded := checkpoint.Batches[batch.ID]; recorded {
			continue
		}
		if _, err := os.Lstat(filepath.Join(directory, "batch-"+batch.ID+".log")); err == nil {
			bundle.Orphans = append(bundle.Orphans, testCampaignBundleOrphan{Batch: batch.ID})
		} else if !os.IsNotExist(err) {
			return bundle, err
		}
	}
	seen := make(map[string]bool)
	key, physical, current := directory, directory, checkpoint
	link, childHash := plan.Recovery, bundle.PlanSHA256
	for index := 0; ; index++ {
		if index > 32 || seen[key] {
			return bundle, errors.New("recovery graph is cyclic or too deep")
		}
		seen[key] = true
		nodePath := fmt.Sprintf("nodes/%d", index)
		bundle.Nodes = append(bundle.Nodes, testCampaignBundleNode{Key: key, Path: nodePath})
		if err := addFile(nodePath+"/checkpoint.json", filepath.Join(physical, "checkpoint.json"), false); err != nil {
			return bundle, err
		}
		if err := addFile(nodePath+"/"+testCampaignRecoverySealName, filepath.Join(physical, testCampaignRecoverySealName), true); err != nil {
			return bundle, err
		}
		ids := make(map[string]bool)
		for id := range current.Batches {
			ids[id] = true
		}
		if index == 0 {
			for _, batch := range plan.Batches {
				if batch.Side == checkpoint.Side {
					ids[batch.ID] = true
				}
			}
		}
		sorted := []string{}
		for id := range ids {
			sorted = append(sorted, id)
		}
		sort.Strings(sorted)
		for _, id := range sorted {
			name := "batch-" + id + ".json"
			if !testCampaignBundleBatchName.MatchString(name) {
				return bundle, errors.New("batch identity is not portable")
			}
			if _, exists := current.Batches[id]; !exists {
				if _, err := os.Lstat(testCampaignJournalDirectory(physical, id)); !os.IsNotExist(err) {
					return bundle, errors.New("uncheckpointed journal requires local inspection before handoff")
				}
			} else {
				if err := collectTestCampaignPortableJournal(nodePath, physical, id, add); err != nil {
					return bundle, err
				}
			}
			path := filepath.Join(physical, name)
			body, err := readTestCampaignBundleBytes(path, testCampaignBundleRawLimit)
			if os.IsNotExist(err) {
				if current.Batches[id].Status == "done" {
					return bundle, errors.New("completed batch report is missing")
				}
				continue
			}
			if err != nil {
				return bundle, err
			}
			var receipts []json.RawMessage
			if decodeTestCampaignBundleJSON(body, &receipts) != nil || len(receipts) == 0 {
				return bundle, errors.New("only complete JSON receipt arrays can be transferred")
			}
			if saved := current.Batches[id]; saved.Status == "done" && saved.ReportSHA256 != testCampaignBundleHash(body) {
				return bundle, errors.New("completed receipt hash changed")
			}
			if err := add(nodePath+"/"+name, body); err != nil {
				return bundle, err
			}
		}
		if link == nil {
			break
		}
		next, err := resolveTestCampaignDirectory(link.ParentDirectory, resolver)
		if err != nil {
			return bundle, err
		}
		var parent testCampaignCheckpoint
		var seal testCampaignRecoverySeal
		if readTestCampaignJSON(filepath.Join(next, "checkpoint.json"), &parent) != nil ||
			readTestCampaignJSON(filepath.Join(next, testCampaignRecoverySealName), &seal) != nil ||
			testCampaignJSONHash(parent) != link.CheckpointSHA256 || parent.PlanSHA256 != link.ParentPlanSHA256 ||
			seal.SchemaVersion != 1 || seal.CheckpointSHA256 != link.CheckpointSHA256 ||
			seal.ParentPlanSHA256 != link.ParentPlanSHA256 || seal.ContinuationSHA256 != childHash ||
			seal.ReconciliationSHA256 != link.ReconciliationSHA256 || seal.Blocked != link.Blocked {
			return bundle, errors.New("parent recovery identity or seal does not match")
		}
		key, physical, current = link.ParentDirectory, next, parent
		if parent.LineageVersion != 1 {
			bundle.HistoryComplete = false
			link = nil
		} else {
			link = parent.Recovery
		}
		childHash = parent.PlanSHA256
	}
	if recovery != "" {
		for _, name := range []string{"reconciliation.json", "continuation-plan.json", "current.json", "baseline.json"} {
			if err := addFile("recovery/"+name, filepath.Join(recovery, name), name == "current.json" || name == "baseline.json"); err != nil {
				return bundle, err
			}
		}
		var reconciliation testCampaignReconciliation
		if err := readTestCampaignJSON(filepath.Join(recovery, "reconciliation.json"), &reconciliation); err != nil {
			return bundle, err
		}
		entries := make(map[string]testCampaignRecoveryBatch)
		for _, entry := range reconciliation.Batches {
			entries[entry.ID] = entry
		}
		for i, batch := range plan.Batches {
			if entry := entries[batch.ID]; entry.RecoveredSHA256 != "" {
				name := fmt.Sprintf("recovered-batch-%d.json", i+1)
				if err := addFile("recovery/"+name, filepath.Join(recovery, name), false); err != nil {
					return bundle, err
				}
			}
		}
	}
	nodeBody, _ := json.Marshal(bundle.Nodes)
	if err := checkTestCampaignBundleSensitive(nodeBody, known); err != nil {
		return bundle, err
	}
	sortedTestCampaignBundleFiles(bundle.Files)
	bundle.ID = testCampaignBundleID(bundle)
	return bundle, validateTestCampaignBundle(bundle)
}

func cmdTestCampaignBundleExport(args []string) int {
	fs := newFlagSet("test policy bundle export", flag.ContinueOnError)
	planPath := fs.String("plan", "", "saved campaign plan")
	policyPath := fs.String("policy", "", "matching campaign policy")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "matching source manifest (never packaged)")
	directory := fs.String("directory", "", "source campaign directory")
	from := fs.String("from", "", "original current report")
	baseline := fs.String("baseline", "", "original baseline report")
	recovery := fs.String("recovery-bundle", "", "optional bound reconciliation bundle")
	output := fs.String("output", "", "new portable bundle JSON file")
	handoff := fs.String("handoff", "", "optional existing import index")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *policyPath == "" || *directory == "" || *output == "" {
		return printErr("Invalid bundle export options", errors.New("--plan, --policy, --directory and --output are required"))
	}
	resolver, err := loadTestCampaignBundleResolver(*handoff)
	if err != nil {
		return printErr("Invalid handoff import", err)
	}
	if err := resolver.requireClaim(true); err != nil {
		return printErr("Handoff is not active", err)
	}
	if err := enforceTestCampaignPlanHandoff(*planPath, resolver); err != nil {
		return printErr("Invalid imported plan", err)
	}
	*directory, err = filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid source directory", err)
	}
	if err := testCampaignPrivateDirectory(*directory); err != nil {
		return printErr("Invalid source directory", err)
	}
	if testCampaignBundleImmutableDirectory(*directory, resolver) {
		return printErr("Immutable import", errors.New("export an owned work directory instead"))
	}
	if err := protectTestCampaignStatusOutputs([]string{*output}, []string{*planPath, *policyPath, *manifestPath, *from, *baseline}, []string{*directory, *recovery}); err != nil {
		return printErr("Invalid bundle output", err)
	}
	if _, err := os.Lstat(*output); !os.IsNotExist(err) {
		return printErr("Bundle destination exists", errors.New("use a new output path"))
	}
	var owner testCampaignBundleOwner
	if err := readTestCampaignJSON(filepath.Join(*directory, testCampaignImportName), &owner); err == nil {
		if resolver == nil || owner != resolver.owner() {
			return printErr("Invalid imported source", errors.New("supply its owning --handoff index"))
		}
	} else if !os.IsNotExist(err) {
		return printErr("Invalid imported source", err)
	}
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(*planPath, &plan); err != nil {
		return printErr("Invalid plan", err)
	}
	releaseHandoff, claimErr := acquireTestCampaignHandoffFor(resolver, *directory, testCampaignJSONHash(plan), "export", true)
	if claimErr != nil {
		return printErr("Handoff claim unavailable", claimErr)
	}
	defer releaseHandoff()
	releaseRunner, err := acquireTestCampaignRunnerLock(*directory, testCampaignJSONHash(plan))
	if err != nil {
		return printErr("Campaign directory is locked", err)
	}
	defer releaseRunner()
	var checkpoint testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(*directory, "checkpoint.json"), &checkpoint); err != nil {
		return printErr("Invalid checkpoint", err)
	}
	transferred := ""
	if resolver != nil {
		var claim testCampaignBundleClaim
		if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil {
			return printErr("Invalid handoff claim", err)
		}
		transferred = claim.Transferred
		if transferred != "" && claim.Work[testCampaignJSONHash(plan)] != *directory {
			return printErr("Invalid frozen source", errors.New("republish only the registered frozen working directory"))
		}
	}
	_, frozenErr := os.Lstat(filepath.Join(*directory, testCampaignHandoffName))
	frozenExport := frozenErr == nil
	if frozenErr != nil && !os.IsNotExist(frozenErr) {
		return printErr("Invalid handoff seal", frozenErr)
	}
	if frozenExport && resolver != nil {
		var claim testCampaignBundleClaim
		if readTestCampaignJSON(resolver.Index.ClaimPath, &claim) != nil || claim.Work[testCampaignJSONHash(plan)] != *directory {
			return printErr("Invalid frozen source", errors.New("working directory ownership is unavailable"))
		}
	}
	if transferred == "" && !frozenExport {
		if err := registerTestCampaignHandoffWork(resolver, testCampaignJSONHash(plan), *directory, checkpoint.Side, checkpoint.Revision); err != nil {
			return printErr("Invalid handoff work directory", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(*directory, testCampaignContinuationOwnerName)); err == nil {
		return printErr("Continuation already owns this campaign", errors.New("export its current working directory instead"))
	} else if !os.IsNotExist(err) {
		return printErr("Could not inspect continuation ownership", err)
	}
	if resolver != nil {
		var seal testCampaignRecoverySeal
		if readTestCampaignJSON(filepath.Join(*directory, testCampaignRecoverySealName), &seal) == nil {
			var claim testCampaignBundleClaim
			if readTestCampaignJSON(resolver.Index.ClaimPath, &claim) != nil {
				return printErr("Invalid handoff claim", errors.New("could not read ownership ledger"))
			}
			if claim.Work[seal.ContinuationSHA256] != "" {
				return printErr("Continuation already started", errors.New("export its current working directory instead"))
			}
		}
	}
	policy, err := readTestCampaignPolicy(*policyPath)
	if err != nil {
		return printErr("Invalid policy", err)
	}
	manifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		return printErr("Invalid manifest", err)
	}
	check := plan
	check.Recovery = nil
	if err := validateTestCampaignRunnable(check, manifest.Scenarios, policy); err != nil {
		return printErr("Bundle inputs changed", err)
	}
	var before, after []testRunReceipt
	if *from != "" {
		after, _, err = readTestBaselineReport(*from, nil)
		if err != nil {
			return printErr("Invalid current input", err)
		}
	}
	if *baseline != "" {
		before, _, err = readTestBaselineReport(*baseline, nil)
		if err != nil {
			return printErr("Invalid baseline input", err)
		}
	}
	report, err := buildTestCampaignStatus(plan, &checkpoint, *directory, "", policy, manifest.Scenarios, before, after, resolver)
	if err != nil {
		return printErr("Invalid campaign state", err)
	}
	for _, issue := range report.Issues {
		if issue.Code == "evidence_conflict" {
			return printErr("Conflicting campaign evidence", errors.New(issue.Message))
		}
	}
	if *recovery != "" {
		if err := applyTestCampaignStatusRecovery(&report, plan, checkpoint, *recovery, policy, manifest.Scenarios, resolver); err != nil {
			return printErr("Invalid recovery bundle", err)
		}
	}
	bundle, err := collectTestCampaignBundle(plan, checkpoint, policy, manifest.Scenarios, *directory, *from, *baseline, *recovery, resolver)
	if err != nil {
		return printErr("Could not collect handoff artifacts", err)
	}
	rawPlan, err := readTestCampaignBundleBytes(*planPath, testCampaignBundleRawLimit)
	if err != nil {
		return printErr("Could not capture saved plan bytes", err)
	}
	knownPlanSecrets := []string{}
	for _, scenario := range manifest.Scenarios {
		for _, secret := range scenario.Secrets {
			knownPlanSecrets = append(knownPlanSecrets, secret)
		}
		for _, service := range scenario.Services {
			for _, secret := range service.Secrets {
				knownPlanSecrets = append(knownPlanSecrets, secret)
			}
		}
	}
	if err := checkTestCampaignBundleSensitive(rawPlan, knownPlanSecrets); err != nil {
		return printErr("Saved plan contains sensitive material", err)
	}
	var capturedPlan testCampaignPolicyPlan
	if decodeTestCampaignBundleJSON(rawPlan, &capturedPlan) != nil || testCampaignJSONHash(capturedPlan) != bundle.PlanSHA256 {
		return printErr("Plan changed during export", errors.New("capture a stable source plan"))
	}
	for i, file := range bundle.Files {
		if file.Path == "plan.json" {
			bundle.Files[i].Data = rawPlan
			bundle.Files[i].SHA256 = testCampaignBundleHash(rawPlan)
		}
	}
	bundle.ID = testCampaignBundleID(bundle)
	if err := validateTestCampaignBundle(bundle); err != nil {
		return printErr("Invalid captured bundle", err)
	}
	var capturedCheckpoint testCampaignCheckpoint
	for _, file := range bundle.Files {
		if file.Path == "nodes/0/checkpoint.json" {
			if decodeTestCampaignBundleJSON(file.Data, &capturedCheckpoint) != nil {
				return printErr("Invalid captured checkpoint", errors.New("checkpoint could not be decoded"))
			}
		}
	}
	if testCampaignJSONHash(capturedCheckpoint) != testCampaignJSONHash(checkpoint) {
		return printErr("Checkpoint changed during export", errors.New("capture a stable source checkpoint"))
	}
	protected := []string{*directory, *recovery}
	for _, node := range bundle.Nodes {
		physical, err := resolveTestCampaignDirectory(node.Key, resolver)
		if err != nil {
			return printErr("Invalid portable parent", err)
		}
		protected = append(protected, physical)
	}
	if resolver != nil {
		protected = append(protected, resolver.Index.Root)
	}
	if err := protectTestCampaignStatusOutputs([]string{*output}, []string{*planPath, *policyPath, *manifestPath, *from, *baseline}, protected); err != nil {
		return printErr("Invalid bundle output", err)
	}
	if transferred != "" && transferred != bundle.ID {
		return printErr("Frozen export changed", errors.New("restore the original frozen artifacts before republishing"))
	}
	seal := testCampaignHandoffSeal{SchemaVersion: 1, BundleID: bundle.ID, PlanSHA256: bundle.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(checkpoint)}
	sealPath := filepath.Join(*directory, testCampaignHandoffName)
	if _, err := os.Lstat(sealPath); err == nil {
		var existing testCampaignHandoffSeal
		if readTestCampaignJSON(sealPath, &existing) != nil || existing != seal {
			return printErr("Campaign was already handed off", errors.New("only an identical frozen export may be republished"))
		}
	} else if !os.IsNotExist(err) {
		return printErr("Could not inspect handoff seal", err)
	}
	// Freeze the source before publishing any importable bytes.
	if err := writeTestCampaignJSON(sealPath, seal); err != nil {
		return printErr("Could not freeze source campaign", err)
	}
	if err := revokeTestCampaignHandoff(resolver, bundle.ID); err != nil {
		return printErr("Source frozen; claim transfer failed", err)
	}
	body, err := json.Marshal(bundle)
	if err != nil {
		return printErr("Could not encode bundle", err)
	}
	file, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return printErr("Source frozen; bundle publication failed", err)
	}
	_, writeErr := file.Write(body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return printErr("Source frozen; bundle publication incomplete", writeErr)
	}
	if closeErr != nil {
		return printErr("Source frozen; bundle publication incomplete", closeErr)
	}
	return writeTestCampaignBundleSummary(bundle)
}
func writeTestCampaignBundleSummary(bundle testCampaignBundle) int {
	journals, runs, uncertain := 0, 0, 0
	for _, file := range bundle.Files {
		_, name, ok := testCampaignPortableJournalPath(file.Path)
		if !ok {
			continue
		}
		if name == "binding.json" {
			journals++
			continue
		}
		if name == "inventory.json" {
			continue
		}
		var journal testCampaignRunJournal
		if json.Unmarshal(file.Data, &journal) != nil {
			continue
		}
		runs++
		for _, workload := range journal.Workloads {
			if workload.State == "creating" {
				uncertain++
			}
		}
	}
	summary := struct {
		SchemaVersion         int    `json:"schema_version"`
		BundleID              string `json:"bundle_id"`
		Files                 int    `json:"files"`
		Nodes                 int    `json:"history_nodes"`
		HistoryComplete       bool   `json:"history_complete"`
		JournalDirectories    int    `json:"journal_directories"`
		JournalRuns           int    `json:"journal_runs"`
		UncertainProvisioning int    `json:"uncertain_provisioning"`
	}{1, bundle.ID, len(bundle.Files), len(bundle.Nodes), bundle.HistoryComplete, journals, runs, uncertain}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return printErr("Could not write bundle summary", err)
	}
	return 0
}
