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
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testCampaignRecoverySealName = "campaign-reconciled.json"

type testCampaignRecoveryLink struct {
	ParentDirectory      string                 `json:"parent_directory"`
	ParentPlanSHA256     string                 `json:"parent_plan_sha256"`
	CheckpointSHA256     string                 `json:"checkpoint_sha256"`
	ReconciliationSHA256 string                 `json:"reconciliation_sha256"`
	Side                 string                 `json:"side"`
	Revision             string                 `json:"revision"`
	Allocation           testCampaignPlanBudget `json:"side_allocation"`
	Reserved             testCampaignPlanBudget `json:"attempted_batch_reservation"`
	Remaining            testCampaignPlanBudget `json:"remaining_budget"`
	Blocked              bool                   `json:"blocked"`
	Reasons              []string               `json:"reasons"`
}

type testCampaignRecoverySeal struct {
	SchemaVersion        int    `json:"schema_version"`
	Blocked              bool   `json:"blocked"`
	ParentPlanSHA256     string `json:"parent_plan_sha256"`
	CheckpointSHA256     string `json:"checkpoint_sha256"`
	ReconciliationSHA256 string `json:"reconciliation_sha256"`
	ContinuationSHA256   string `json:"continuation_plan_sha256"`
}

type testCampaignRecoveryEvidence struct {
	ParentPlanSHA256     string `json:"parent_plan_sha256"`
	CheckpointSHA256     string `json:"checkpoint_sha256"`
	BatchID              string `json:"batch_id"`
	OriginalReportSHA256 string `json:"original_report_sha256"`
	ReceiptPath          string `json:"receipt_path"`
}

type testCampaignRecoveryObservation struct {
	Project    string    `json:"project"`
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	ObservedAt time.Time `json:"observed_at"`
}

type testCampaignRecoveryBatch struct {
	ID               string                            `json:"id"`
	CheckpointStatus string                            `json:"checkpoint_status"`
	Status           string                            `json:"status"`
	ReportSHA256     string                            `json:"report_sha256,omitempty"`
	RecoveredSHA256  string                            `json:"recovered_report_sha256,omitempty"`
	JournalSHA256    string                            `json:"journal_sha256,omitempty"`
	AdoptedRuns      int                               `json:"adopted_runs"`
	SkippedRuns      int                               `json:"skipped_runs"`
	Cleanup          string                            `json:"cleanup"`
	Reasons          []string                          `json:"reasons"`
	Live             []testCampaignRecoveryObservation `json:"live_cleanup,omitempty"`
}

type testCampaignReconciliation struct {
	SchemaVersion    int                         `json:"schema_version"`
	ParentPlanSHA256 string                      `json:"parent_plan_sha256"`
	CheckpointSHA256 string                      `json:"checkpoint_sha256"`
	Side             string                      `json:"side"`
	Revision         string                      `json:"revision"`
	Ready            bool                        `json:"ready"`
	Allocation       testCampaignPlanBudget      `json:"side_allocation"`
	Reserved         testCampaignPlanBudget      `json:"attempted_batch_reservation"`
	Remaining        testCampaignPlanBudget      `json:"remaining_budget"`
	Batches          []testCampaignRecoveryBatch `json:"batches"`
	Reasons          []string                    `json:"reasons"`
}

type testCampaignCleanupReader interface {
	GetDevSession(context.Context, string, string) (api.DevSessionResponse, error)
}

func validateTestCampaignRecoverySeal(plan testCampaignPolicyPlan, resolvers ...*testCampaignBundleResolver) error {
	link := plan.Recovery
	if link == nil {
		return nil
	}
	if link.Blocked {
		return errors.New("continuation is blocked by unresolved recovery evidence")
	}
	parentDirectory, err := resolveTestCampaignDirectory(link.ParentDirectory, resolvers...)
	if err != nil {
		return err
	}
	var resolver *testCampaignBundleResolver
	if len(resolvers) == 1 {
		resolver = resolvers[0]
	}
	if err := checkTestCampaignBundleWork(parentDirectory, resolver, false); err != nil {
		return err
	}
	if err := resolver.requireActive(); err != nil {
		return err
	}
	var seal testCampaignRecoverySeal
	if err := readTestCampaignJSON(filepath.Join(parentDirectory, testCampaignRecoverySealName), &seal); err != nil {
		return errors.New("parent reconciliation seal is unavailable; continuation cannot execute")
	}
	var checkpoint testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(parentDirectory, "checkpoint.json"), &checkpoint); err != nil {
		return errors.New("parent checkpoint is unavailable")
	}
	if seal.SchemaVersion != 1 || seal.Blocked || seal.ContinuationSHA256 != testCampaignJSONHash(plan) ||
		seal.ParentPlanSHA256 != link.ParentPlanSHA256 || seal.CheckpointSHA256 != link.CheckpointSHA256 ||
		seal.ReconciliationSHA256 != link.ReconciliationSHA256 || testCampaignJSONHash(checkpoint) != link.CheckpointSHA256 ||
		checkpoint.PlanSHA256 != link.ParentPlanSHA256 || checkpoint.Side != link.Side || checkpoint.Revision != link.Revision {
		return errors.New("continuation no longer matches the sealed parent campaign")
	}
	return nil
}

func testCampaignAddRecoveryCost(total *testCampaignPlanBudget, cost testCampaignPlanBudget) error {
	for _, d := range []struct {
		n   int64
		sum *int64
	}{{cost.Runs, &total.Runs}, {cost.Deployments, &total.Deployments}, {cost.WorkloadMinutes, &total.WorkloadMinutes}} {
		if d.n < 0 || d.n > int64(^uint64(0)>>1)-*d.sum {
			return errors.New("recovery cost overflow")
		}
		*d.sum += d.n
	}
	return nil
}

func testCampaignRecoveryRemaining(allocation, reserved testCampaignPlanBudget) (testCampaignPlanBudget, error) {
	if allocation.Runs < 0 || allocation.Deployments < 0 || allocation.WorkloadMinutes < 0 || reserved.Runs < 0 || reserved.Deployments < 0 || reserved.WorkloadMinutes < 0 || reserved.Runs > allocation.Runs || reserved.Deployments > allocation.Deployments || reserved.WorkloadMinutes > allocation.WorkloadMinutes {
		return testCampaignPlanBudget{}, errors.New("recovery reservation exceeds the original side allocation")
	}
	return testCampaignPlanBudget{Runs: allocation.Runs - reserved.Runs, Deployments: allocation.Deployments - reserved.Deployments,
		WorkloadMinutes: allocation.WorkloadMinutes - reserved.WorkloadMinutes}, nil
}

func testCampaignRecoveryEnded(r testRunReceipt) bool {
	return !r.StartedAt.IsZero() && !r.FinishedAt.IsZero() && !r.FinishedAt.Before(r.StartedAt) &&
		(r.Status == "passed" || r.Status == "failed" || r.Status == "skipped")
}

func testCampaignRecoverableLeaf(r testRunReceipt, batch testCampaignCostBatch, scenario testScenario, revision string) error {
	if r.Scenario != batch.Scenario || r.Engine != "real-vm" || r.Profile != batch.Profile || !testCampaignRecoveryEnded(r) {
		return errors.New("leaf context or completion evidence is unavailable")
	}
	state := testReportMergeState{SourceIdentities: make(map[testReportSourceContext]string), RunIDs: make(map[string]string)}
	if err := state.checkReceipt(r, "recovery leaf", 0); err != nil {
		return errors.New("leaf identity or source evidence is incomplete")
	}
	candidates, err := testCampaignPlanCandidates(scenario)
	if err != nil {
		return err
	}
	matched := false
	for _, candidate := range candidates {
		if candidate.Case == r.Case && candidate.Hash == testDigestScopeFingerprint([]testRunReceipt{r}) {
			matched = true
		}
	}
	if !matched {
		return errors.New("leaf configuration is not part of the saved batch")
	}
	names := []string{scenario.Project}
	for name, spec := range scenario.Services {
		if spec.Fixture == "" {
			names = append(names, name)
		}
	}
	for _, name := range names {
		source, ok := r.SourceRevision.Workloads[name]
		if !ok || source.GitCommit != revision || source.TrackedChanges == nil || *source.TrackedChanges {
			return errors.New("leaf source revision differs from the campaign revision")
		}
	}
	return nil
}

func testCampaignLiveCleanup(ctx context.Context, reader testCampaignCleanupReader, scenario testScenario, leaves []testRunReceipt) ([]testCampaignRecoveryObservation, bool) {
	observations := []testCampaignRecoveryObservation{}
	seen := make(map[string]bool)
	clean := len(leaves) > 0
	projects := []string{scenario.Project}
	for name := range scenario.Services {
		projects = append(projects, scenario.Project+"-"+name)
	}
	sort.Strings(projects)
	// Only GET session inspection is available through this interface.
	for _, leaf := range leaves {
		if leaf.Status == "skipped" && leaf.RunID == "" {
			continue
		}
		for _, project := range projects {
			key := project + "/" + leaf.RunID
			if seen[key] {
				continue
			}
			seen[key] = true
			observation := testCampaignRecoveryObservation{Project: project, RunID: leaf.RunID, Status: "unknown", ObservedAt: time.Now().UTC()}
			decoded, err := hex.DecodeString(leaf.RunID)
			if err == nil && len(decoded) == 16 && hex.EncodeToString(decoded) == leaf.RunID && len(observations) < 1024 {
				_, err := reader.GetDevSession(ctx, project, leaf.RunID)
				if isNotFound(err) {
					observation.Status = "absent"
				} else if err == nil {
					observation.Status = "present"
				}
			}
			if observation.Status != "absent" {
				clean = false
			}
			observations = append(observations, observation)
			if len(observations) >= 1024 {
				return observations, false
			}
		}
	}
	return observations, clean
}

// Recovered raw objects retain assertion/source evidence. Flattening partial
// native hierarchies adds explicit original paths rather than fabricating runs.
func inspectTestCampaignRecoveryReport(ctx context.Context, path string, batch testCampaignCostBatch, scenario testScenario,
	checkpoint testCampaignCheckpoint, saved testCampaignCheckpointBatch, reader testCampaignCleanupReader) (result testCampaignRecoveryBatch, recovered []json.RawMessage) {
	result = testCampaignRecoveryBatch{ID: batch.ID, CheckpointStatus: saved.Status, Status: "blocked", Cleanup: "unknown", Reasons: []string{}}
	reasonSet := make(map[string]bool)
	block := func(reason string) {
		if !reasonSet[reason] && len(result.Reasons) < 50 {
			reasonSet[reason] = true
			result.Reasons = append(result.Reasons, reason)
		}
	}
	// Missing reports stay blocked; journal observations never become receipt evidence.
	defer func() {
		if len(result.Reasons) == 0 || saved.Status == "done" {
			return
		}
		journalDirectory := testCampaignJournalDirectory(filepath.Dir(path), batch.ID)
		if _, err := os.Lstat(journalDirectory); os.IsNotExist(err) {
			return
		}
		targets, hash, err := readTestCampaignJournalTargets(filepath.Dir(path), checkpoint.PlanSHA256, checkpoint, batch, scenario)
		if err != nil {
			block("run journal is incomplete or uncertain; inspect locally")
			return
		}
		result.JournalSHA256 = hash
		block("run journal provides cleanup identities only; missing execution evidence remains blocking")
		if reader == nil {
			return
		}
		keys := make([]string, 0, len(targets))
		for key := range targets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if len(result.Live) >= 1024 {
				block("journal inspection exceeds workload limit")
				return
			}
			target := targets[key]
			observation := testCampaignRecoveryObservation{Project: target.Project, RunID: target.RunID, Status: "unknown", ObservedAt: time.Now().UTC()}
			session, err := reader.GetDevSession(ctx, target.Project, target.RunID)
			if isNotFound(err) {
				observation.Status = "absent"
			} else if err == nil {
				if session.App.ID == target.AppID {
					observation.Status = "present"
				} else {
					observation.Status = "identity_changed"
				}
			}
			result.Live = append(result.Live, observation)
		}
	}()
	file, err := openCustomerFile(path)
	if err != nil {
		block("batch report is missing or unreadable")
		return result, nil
	}
	body, err := io.ReadAll(io.LimitReader(file, testBaselineReportLimit+1))
	_ = file.Close()
	if err != nil || len(body) > testBaselineReportLimit {
		block("batch report is unreadable or exceeds 64 MiB")
		return result, nil
	}
	sum := sha256.Sum256(body)
	result.ReportSHA256 = hex.EncodeToString(sum[:])
	if saved.Status == "done" && (saved.ReportSHA256 == "" || saved.ReportSHA256 != result.ReportSHA256) {
		block("completed report hash differs from checkpoint")
		return result, nil
	}
	var roots []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if decoder.Decode(&roots) != nil || decoder.Decode(new(any)) != io.EOF || len(roots) == 0 {
		block("batch report is not a complete receipt array")
		return result, nil
	}
	fullHash, fullCleanup, fullErr := validateTestCampaignBatchReport(path, batch, scenario, checkpoint.Revision)
	full := fullErr == nil && !fullCleanup && fullHash == result.ReportSHA256
	envelope := len(roots) == batch.Repetitions
	cleanupFailed := false
	adopted := []json.RawMessage{}
	leaves := []testRunReceipt{}
	expected := make(map[[2]string]int)
	candidates, err := testCampaignPlanCandidates(scenario)
	if err != nil {
		block("manifest candidates are unavailable")
		return result, nil
	}
	for _, candidate := range candidates {
		expected[[2]string{candidate.Case, candidate.Hash}] += candidate.Runs * batch.Repetitions
	}
	nodes := 0
	seenRuns := make(map[string]bool)
	var walk func(json.RawMessage, string, string, int, bool)
	walk = func(raw json.RawMessage, receiptPath, inheritedCase string, depth int, inheritedProvenance bool) {
		nodes++
		if depth > 32 || nodes > 100000 {
			block("receipt hierarchy exceeds recovery bounds")
			return
		}
		var r testRunReceipt
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &r) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
			block("receipt object is invalid")
			return
		}
		if r.Case == "" {
			r.Case = inheritedCase
		}
		if r.CleanupError != "" {
			cleanupFailed = true
		}
		if r.FaultMatrix != nil && r.Resilience != nil {
			block("receipt hierarchy is ambiguous")
			return
		}
		if r.FaultMatrix != nil || r.Resilience != nil {
			var container map[string]json.RawMessage
			key, childKey := "fault_matrix", "cases"
			if r.Resilience != nil {
				key, childKey = "resilience", "history"
			}
			var children []json.RawMessage
			if json.Unmarshal(fields[key], &container) != nil || json.Unmarshal(container[childKey], &children) != nil || len(children) == 0 {
				block("receipt hierarchy is incomplete")
				return
			}
			for i, child := range children {
				walk(child, fmt.Sprintf("%s/%s/%d", receiptPath, childKey, i), r.Case, depth+1, inheritedProvenance || len(r.ReportProvenance) > 0)
			}
			return
		}
		leaves = append(leaves, r)
		if r.Status == "skipped" {
			result.SkippedRuns++
			if r.RunID != "" || r.SkipReason == "" || !testCampaignRecoveryEnded(r) {
				block("skipped receipt does not prove that work remained unstarted")
			}
			return
		}
		if err := testCampaignRecoverableLeaf(r, batch, scenario, checkpoint.Revision); err != nil {
			block(err.Error())
			return
		}
		if seenRuns[r.RunID] {
			block("batch report repeats an executed run ID")
			return
		}
		seenRuns[r.RunID] = true
		if inheritedProvenance && !full {
			block("partial recovery cannot flatten premerged ancestor provenance")
			return
		}
		key := [2]string{r.Case, testDigestScopeFingerprint([]testRunReceipt{r})}
		if expected[key] <= 0 {
			block("report contains more executions than the saved batch")
			return
		}
		expected[key]--
		if r.Case != "" {
			fields["case"], _ = json.Marshal(r.Case)
		}
		provenance := append([]testCampaignRecoveryEvidence{}, r.CampaignRecovery...)
		if len(provenance) >= 64 {
			block("recovery history exceeds limit")
			return
		}
		provenance = append(provenance, testCampaignRecoveryEvidence{ParentPlanSHA256: checkpoint.PlanSHA256,
			CheckpointSHA256: testCampaignJSONHash(checkpoint), BatchID: batch.ID, OriginalReportSHA256: result.ReportSHA256, ReceiptPath: receiptPath})
		fields["campaign_recovery"], _ = json.Marshal(provenance)
		encoded, err := json.Marshal(fields)
		if err != nil {
			block("could not preserve recovered raw evidence")
			return
		}
		adopted = append(adopted, encoded)
		result.AdoptedRuns++
	}
	for i, raw := range roots {
		var root testRunReceipt
		if json.Unmarshal(raw, &root) != nil || root.Scenario != batch.Scenario || root.Profile != batch.Profile || root.Engine != "real-vm" || !testCampaignRecoveryEnded(root) {
			envelope = false
		}
		walk(raw, fmt.Sprintf("roots/%d", i), "", 0, false)
	}
	if !envelope {
		block("report does not prove that the native batch finished")
	}
	if cleanupFailed {
		result.Cleanup = "recorded_failure"
		block("recorded cleanup failure requires manual reconciliation")
	} else if envelope {
		result.Cleanup = "recorded_clean"
	}
	if reader != nil {
		var clean bool
		result.Live, clean = testCampaignLiveCleanup(ctx, reader, scenario, leaves)
		if !clean {
			result.Cleanup = "live_unresolved"
			block("live workload cleanup is present or unavailable")
		} else if !cleanupFailed && envelope {
			result.Cleanup = "live_absent"
		}
	}
	if len(result.Reasons) == 0 && full {
		adopted = []json.RawMessage{}
		for i, raw := range roots {
			var fields map[string]json.RawMessage
			var root testRunReceipt
			_ = json.Unmarshal(raw, &fields)
			_ = json.Unmarshal(raw, &root)
			if len(root.CampaignRecovery) >= 64 {
				block("recovery history exceeds limit")
				break
			}
			provenance := append([]testCampaignRecoveryEvidence{}, root.CampaignRecovery...)
			provenance = append(provenance, testCampaignRecoveryEvidence{ParentPlanSHA256: checkpoint.PlanSHA256,
				CheckpointSHA256: testCampaignJSONHash(checkpoint), BatchID: batch.ID, OriginalReportSHA256: result.ReportSHA256, ReceiptPath: fmt.Sprintf("roots/%d", i)})
			fields["campaign_recovery"], _ = json.Marshal(provenance)
			encoded, _ := json.Marshal(fields)
			adopted = append(adopted, encoded)
		}
	}
	if len(result.Reasons) == 0 {
		result.Status = "recovered"
		if result.SkippedRuns > 0 {
			result.Status = "partially_recovered"
		}
	}
	return result, adopted
}

func clearTestCampaignRecoveryBatches(plan *testCampaignPolicyPlan) {
	plan.Batches = []testCampaignCostBatch{}
	plan.Costs.Estimated = testCampaignPlanBudget{}
	for i := range plan.Entries {
		entry := &plan.Entries[i]
		entry.Suggestions = []testCampaignPlanCommand{}
		entry.Planned = &testCampaignPlannedCoverage{BaselineRemaining: entry.BaselineAdditional,
			CurrentRemaining: entry.CurrentAdditional, BatchIDs: []string{}}
	}
}

func buildTestCampaignContinuation(parent testCampaignPolicyPlan, checkpoint testCampaignCheckpoint, reconciliation testCampaignReconciliation,
	policy testCampaignPolicy, scenarios map[string]testScenario, manifestPath, parentDirectory string, before, after []testRunReceipt) (testCampaignPolicyPlan, error) {
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, manifestPath, before, after)
	if err != nil {
		return plan, err
	}
	// A continuation stays on the reconciled revision side.
	for i := range plan.Entries {
		commands := []testCampaignPlanCommand{}
		for _, command := range plan.Entries[i].Suggestions {
			if command.Side == checkpoint.Side {
				commands = append(commands, command)
			}
		}
		plan.Entries[i].Suggestions = commands
	}
	if err := optimizeTestCampaignPlan(&plan, scenarios, manifestPath, reconciliation.Remaining); err != nil {
		return plan, err
	}
	plan.Execution = &testCampaignExecutionBinding{ManifestSHA256: parent.Execution.ManifestSHA256, PolicySHA256: parent.Execution.PolicySHA256}
	plan.Recovery = &testCampaignRecoveryLink{ParentDirectory: parentDirectory, ParentPlanSHA256: checkpoint.PlanSHA256,
		CheckpointSHA256: testCampaignJSONHash(checkpoint), ReconciliationSHA256: testCampaignJSONHash(reconciliation),
		Side: checkpoint.Side, Revision: checkpoint.Revision, Allocation: reconciliation.Allocation, Reserved: reconciliation.Reserved,
		Remaining: reconciliation.Remaining, Blocked: !reconciliation.Ready, Reasons: append([]string{}, reconciliation.Reasons...)}
	// Zero is unlimited in ordinary plan flags, but an exhausted recovery
	// allocation is a hard zero and must never reopen the budget.
	if !reconciliation.Ready || reconciliation.Remaining.Runs == 0 || reconciliation.Remaining.Deployments == 0 || reconciliation.Remaining.WorkloadMinutes == 0 {
		clearTestCampaignRecoveryBatches(&plan)
	}
	plan.Notes = append(plan.Notes, "Recovery reserves every attempted batch at full estimated cost. Only this side's unspent original allocation is available; no new budget is granted.")
	return plan, nil
}
