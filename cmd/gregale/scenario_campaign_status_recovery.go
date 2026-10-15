package main

import (
	"errors"
	"fmt"
	"path/filepath"
)

func applyTestCampaignStatusRecovery(report *testCampaignStatusReport, plan testCampaignPolicyPlan, checkpoint testCampaignCheckpoint,
	bundle string, policy testCampaignPolicy, scenarios map[string]testScenario, resolvers ...*testCampaignBundleResolver) error {
	var reconciliation testCampaignReconciliation
	var continuation testCampaignPolicyPlan
	var seal testCampaignRecoverySeal
	if err := readTestCampaignJSON(filepath.Join(bundle, "reconciliation.json"), &reconciliation); err != nil {
		return err
	}
	if err := readTestCampaignJSON(filepath.Join(bundle, "continuation-plan.json"), &continuation); err != nil {
		return err
	}
	if err := readTestCampaignJSON(filepath.Join(report.Directory, testCampaignRecoverySealName), &seal); err != nil {
		return err
	}
	link := continuation.Recovery
	parentMatches := false
	if link != nil {
		resolved, err := resolveTestCampaignDirectory(link.ParentDirectory, resolvers...)
		parentMatches = err == nil && filepath.Clean(resolved) == filepath.Clean(report.Directory)
		if len(resolvers) == 1 && resolvers[0] != nil && len(resolvers[0].Index.Nodes) > 0 && link.ParentDirectory == resolvers[0].Index.Nodes[0].Key && filepath.Clean(report.Directory) == filepath.Join(resolvers[0].Index.Root, "work") {
			parentMatches = true
		}
	}
	if link == nil || seal.SchemaVersion != 1 || seal.ParentPlanSHA256 != report.PlanSHA256 ||
		seal.CheckpointSHA256 != testCampaignJSONHash(checkpoint) || seal.ContinuationSHA256 != testCampaignJSONHash(continuation) ||
		seal.ReconciliationSHA256 != testCampaignJSONHash(reconciliation) || seal.Blocked == reconciliation.Ready ||
		link.Blocked != seal.Blocked || link.Side != checkpoint.Side || link.Revision != checkpoint.Revision || link.ParentPlanSHA256 != report.PlanSHA256 ||
		link.CheckpointSHA256 != seal.CheckpointSHA256 || link.ReconciliationSHA256 != seal.ReconciliationSHA256 ||
		!parentMatches ||
		reconciliation.ParentPlanSHA256 != report.PlanSHA256 || reconciliation.CheckpointSHA256 != seal.CheckpointSHA256 ||
		reconciliation.Side != checkpoint.Side || reconciliation.Revision != checkpoint.Revision ||
		continuation.Execution == nil || continuation.Execution.PolicySHA256 != plan.Execution.PolicySHA256 ||
		continuation.Execution.ManifestSHA256 != plan.Execution.ManifestSHA256 {
		return errors.New("recovery bundle does not match this campaign's checkpoint and seal")
	}
	remaining, err := testCampaignRecoveryRemaining(reconciliation.Allocation, reconciliation.Reserved)
	if err != nil || reconciliation.Allocation != report.Allocation || remaining != reconciliation.Remaining ||
		link.Allocation != reconciliation.Allocation || link.Reserved != reconciliation.Reserved || link.Remaining != remaining {
		return errors.New("recovery bundle allocation is inconsistent")
	}
	rows := make(map[string]testCampaignRecoveryBatch)
	for _, row := range reconciliation.Batches {
		if _, duplicate := rows[row.ID]; duplicate {
			return errors.New("recovery bundle repeats a batch")
		}
		rows[row.ID] = row
	}
	var reserved testCampaignPlanBudget
	selected := 0
	for index, batch := range plan.Batches {
		if batch.Side != checkpoint.Side {
			continue
		}
		selected++
		recovered, ok := rows[batch.ID]
		if !ok {
			return errors.New("recovery bundle omits a batch")
		}
		switch recovered.Status {
		case "pending":
			if recovered.AdoptedRuns != 0 || recovered.RecoveredSHA256 != "" {
				return errors.New("pending recovery batch claims execution evidence")
			}
		case "recovered", "partially_recovered", "blocked":
			if err := testCampaignAddRecoveryCost(&reserved, batch.Estimated); err != nil {
				return err
			}
		default:
			return errors.New("unknown recovery batch status")
		}
		if recovered.AdoptedRuns > 0 || recovered.RecoveredSHA256 != "" {
			receipts, hash, err := readTestBaselineReport(filepath.Join(bundle, fmt.Sprintf("recovered-batch-%d.json", index+1)), nil)
			if err != nil || hash != recovered.RecoveredSHA256 {
				return errors.New("recovered batch artifact changed")
			}
			var leaves []testRunReceipt
			for _, root := range receipts {
				collectTestFailureLeaves(root, "", &leaves)
			}
			if len(leaves) != recovered.AdoptedRuns {
				return errors.New("recovery adoption count differs from receipts")
			}
			for _, leaf := range leaves {
				if err := testCampaignRecoverableLeaf(leaf, batch, scenarios[batch.Scenario], checkpoint.Revision); err != nil {
					return err
				}
			}
		}
		for rowIndex := range report.Batches {
			row := &report.Batches[rowIndex]
			if row.ID != batch.ID {
				continue
			}
			if recovered.Status != "pending" {
				row.Status, row.Evidence, row.Cleanup, row.ExecutedRuns = "recovered", "verified_recovery_bundle", recovered.Cleanup, recovered.AdoptedRuns
				row.Action = "Use the sealed continuation plan; adopted runs do not need to be repeated."
				if recovered.Status == "blocked" {
					row.Status = "blocked"
					row.Action = "Resolve the recovery diagnostics and refresh the blocked reconciliation bundle."
				}
			}
		}
	}
	if selected != len(rows) || reserved != reconciliation.Reserved {
		return errors.New("recovery reservation differs from batch states")
	}
	var before, after []testRunReceipt
	for _, input := range []struct {
		side, hash string
		receipts   *[]testRunReceipt
	}{
		{"baseline", continuation.Execution.BaselineSHA256, &before}, {"current", continuation.Execution.CurrentSHA256, &after},
	} {
		if input.hash == "" {
			continue
		}
		var hash string
		*input.receipts, hash, err = readTestBaselineReport(filepath.Join(bundle, input.side+".json"), nil)
		if err != nil || hash != input.hash {
			return errors.New("merged recovery evidence changed")
		}
		if _, err := appendTestCampaignStatusEvidence(nil, *input.receipts); err != nil {
			return err
		}
	}
	result, err := evaluateTestCampaignPolicy(policy, before, after, "", "")
	if err != nil {
		return err
	}
	report.RecoveryBundle, report.ContinuationSHA256 = bundle, testCampaignJSONHash(continuation)
	report.Reserved, report.Remaining = reconciliation.Reserved, reconciliation.Remaining
	report.Counts = newTestCampaignStatusCounts()
	for _, row := range report.Batches {
		report.Counts[row.Status]++
	}
	applyTestCampaignStatusPolicy(report, result, plan)
	report.Issues = append(report.Issues, testCampaignStatusIssue{Code: "sealed_recovery", Message: "This recovery bundle is bound to the original campaign seal.", Action: "Use continuation-plan.json with the bundle's report inputs and a fresh checkpoint directory."})
	return nil
}
