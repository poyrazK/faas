package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type testCampaignRecoveryObservationLink struct {
	Path              string `json:"path"`
	SHA256            string `json:"sha256,omitempty"`
	BatchID           string `json:"batch_id,omitempty"`
	Status            string `json:"status"`
	OperationComplete bool   `json:"operation_complete"`
}
type testCampaignRecoveryCompletionBatch struct {
	ID                       string                         `json:"id"`
	ResourceEvidenceComplete bool                           `json:"resource_evidence_complete"`
	KnownSessionsAbsent      bool                           `json:"known_sessions_absent"`
	ExecutionComplete        bool                           `json:"execution_complete"`
	RecordedCleanupFailure   bool                           `json:"recorded_cleanup_failure"`
	ReportSHA256             string                         `json:"report_sha256,omitempty"`
	JournalSHA256            string                         `json:"journal_sha256,omitempty"`
	Resources                []testCampaignRecoveryResource `json:"resources"`
	Blockers                 []string                       `json:"blockers"`
}
type testCampaignRecoveryCompletion struct {
	SchemaVersion            int                                   `json:"schema_version"`
	ReadOnly                 bool                                  `json:"read_only"`
	ObservedAt               time.Time                             `json:"observed_at"`
	PlanSHA256               string                                `json:"plan_sha256"`
	CheckpointSHA256         string                                `json:"checkpoint_sha256"`
	EvidenceSnapshotSHA256   string                                `json:"evidence_snapshot_sha256"`
	SnapshotStable           bool                                  `json:"snapshot_stable"`
	ResourceEvidenceComplete bool                                  `json:"resource_evidence_complete"`
	KnownSessionsAbsent      bool                                  `json:"known_sessions_absent"`
	TestExecutionComplete    bool                                  `json:"test_execution_complete"`
	PolicyPassed             bool                                  `json:"policy_passed"`
	RecoveryComplete         bool                                  `json:"recovery_complete"`
	LiveLookups              int                                   `json:"live_lookups"`
	LinkedObservations       int                                   `json:"linked_observations"`
	Observations             []testCampaignRecoveryObservationLink `json:"observations"`
	Batches                  []testCampaignRecoveryCompletionBatch `json:"batches"`
	Blockers                 []string                              `json:"blockers"`
	Notes                    []string                              `json:"notes"`
}
type testCampaignCompletionEvidence struct {
	receiptHash, journalHash       string
	receiptTargets, journalTargets map[string]testCampaignCleanupAction
}

func linkTestCampaignCleanupObservation(path string, plan testCampaignPolicyPlan, cp testCampaignCheckpoint, evidence map[string]testCampaignCompletionEvidence) testCampaignRecoveryObservationLink {
	link := testCampaignRecoveryObservationLink{Path: path, Status: "invalid"}
	body, err := readTestCampaignBundleBytes(path, 8*1024*1024)
	if err != nil {
		return link
	}
	link.SHA256 = testCampaignBundleHash(body)
	var observation testCampaignCleanupObservation
	if checkTestCampaignBundleJSONKeys(body) != nil || decodeTestCampaignBundleJSON(body, &observation) != nil {
		return link
	}
	if observation.SchemaVersion == 1 {
		link.Status = "legacy_unbound"
		return link
	}
	if observation.SchemaVersion != 2 || observation.ObservedAt.IsZero() {
		return link
	}
	if observation.PlanSHA256 != testCampaignJSONHash(plan) || observation.CheckpointSHA256 != testCampaignJSONHash(cp) ||
		observation.Side != cp.Side || observation.Revision != cp.Revision {
		link.Status = "unrelated_checkpoint"
		return link
	}
	current, exists := evidence[observation.BatchID]
	if !exists {
		link.Status = "unrelated_batch"
		return link
	}
	link.BatchID = observation.BatchID
	var targets map[string]testCampaignCleanupAction
	switch observation.Evidence {
	case "receipt":
		if current.receiptHash == "" || current.receiptHash != observation.ReportSHA256 {
			link.Status = "stale_evidence"
			return link
		}
		targets = current.receiptTargets
	case "journal":
		if current.journalHash == "" || current.journalHash != observation.JournalAfterSHA256 || observation.JournalSHA256 == "" {
			link.Status = "stale_evidence"
			return link
		}
		targets = current.journalTargets
	default:
		return link
	}
	if len(targets) == 0 || len(targets) > 1024 || observation.TargetsSHA256 != testCampaignCleanupTargetsSHA256(targets) {
		link.Status = "identity_mismatch"
		return link
	}
	seen := map[string]bool{}
	for _, action := range observation.Actions {
		key := action.Project + "/" + action.RunID
		target, ok := targets[key]
		if !ok || seen[key] || target.AppID != action.AppID {
			link.Status = "identity_mismatch"
			return link
		}
		seen[key] = true
		switch action.Status {
		case "present", "absent", "deleted", "delete_failed", "unconfirmed", "identity_changed":
		default:
			return link
		}
		if !observation.Executed && action.Status == "deleted" {
			return link
		}
		if observation.Executed && observation.OperationComplete && action.Status == "present" {
			return link
		}
		if observation.OperationComplete && action.Status != "present" && action.Status != "absent" && action.Status != "deleted" {
			return link
		}
	}
	link.OperationComplete = observation.OperationComplete && len(seen) == len(targets)
	if !link.OperationComplete {
		link.Status = "linked_partial"
		return link
	}
	link.Status = "linked"
	return link
}

func buildTestCampaignRecoveryCompletion(ctx context.Context, assessment testCampaignRecoveryAssessment, plan testCampaignPolicyPlan,
	cp testCampaignCheckpoint, scenarios map[string]testScenario, directory, stamp string, paths []string, reader testCampaignCleanupReader, resolver *testCampaignBundleResolver) testCampaignRecoveryCompletion {
	report := testCampaignRecoveryCompletion{SchemaVersion: 1, ReadOnly: true, ObservedAt: time.Now().UTC(), PlanSHA256: testCampaignJSONHash(plan),
		CheckpointSHA256: testCampaignJSONHash(cp), EvidenceSnapshotSHA256: stamp, SnapshotStable: assessment.SnapshotStable,
		ResourceEvidenceComplete: true, KnownSessionsAbsent: reader != nil, TestExecutionComplete: true, PolicyPassed: assessment.Status.PolicyPassed,
		Observations: []testCampaignRecoveryObservationLink{}, Batches: []testCampaignRecoveryCompletionBatch{}, Blockers: []string{}, Notes: []string{
			"Cleanup observations are provenance links, never proof of current absence. Every known session is rechecked with GET.",
			"Session absence concerns workload sessions only. Fixture/consumer cleanup failures and missing execution reports remain independent blockers.",
			"Legacy, partial, stale and unrelated observations cannot establish completion. Their status is reported without granting evidence.",
			"This summary is a bounded read-only snapshot; it grants no ownership, coverage, budget refund or authority to waive failures.",
		}}
	rows := map[string]testCampaignRecoveryAssessmentBatch{}
	for _, row := range assessment.Batches {
		rows[row.ID] = row
	}
	statuses := map[string]testCampaignStatusBatch{}
	for _, row := range assessment.Status.Batches {
		statuses[row.ID] = row
	}
	evidence := map[string]testCampaignCompletionEvidence{}
	seenRuns := map[string]string{}
	for _, batch := range plan.Batches {
		if batch.Side != cp.Side {
			continue
		}
		state := statuses[batch.ID]
		complete := state.Status == "completed" || state.Status == "recovered"
		if !complete {
			report.TestExecutionComplete = false
		}
		saved, attempted := cp.Batches[batch.ID]
		if !attempted {
			for _, name := range []string{"batch-" + batch.ID + ".json", "batch-" + batch.ID + ".log", "batch-" + batch.ID + ".journal"} {
				if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
					report.ResourceEvidenceComplete = false
					report.Blockers = append(report.Blockers, batch.ID+": uncheckpointed artifacts require manual resource inspection.")
					break
				}
			}
			continue
		}
		row := rows[batch.ID]
		item := testCampaignRecoveryCompletionBatch{ID: batch.ID, ExecutionComplete: complete, KnownSessionsAbsent: reader != nil,
			Resources: []testCampaignRecoveryResource{}, Blockers: []string{}}
		source := testCampaignCompletionEvidence{}
		targets := map[string]testCampaignCleanupAction{}
		if row.Evidence == "receipt" {
			source.receiptHash = row.EvidenceSHA256
			source.receiptTargets = map[string]testCampaignCleanupAction{}
			for _, resource := range row.Resources {
				action := testCampaignCleanupAction{Project: resource.Project, RunID: resource.RunID, AppID: resource.AppID}
				targets[action.Project+"/"+action.RunID] = action
				source.receiptTargets[action.Project+"/"+action.RunID] = action
			}
			item.ResourceEvidenceComplete = len(targets) > 0
			item.ReportSHA256 = row.EvidenceSHA256
		}
		journalDirectory := testCampaignJournalDirectory(directory, batch.ID)
		if _, err := os.Lstat(journalDirectory); !os.IsNotExist(err) {
			journalTargets, hash, err := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenarios[batch.Scenario])
			if err != nil {
				item.ResourceEvidenceComplete = false
				item.Blockers = append(item.Blockers, "Journal has missing, malformed or uncertain provisioning evidence; absence of known sessions cannot resolve it.")
			} else {
				item.ResourceEvidenceComplete = true
				source.journalHash, source.journalTargets = hash, journalTargets
				item.JournalSHA256 = hash
				for key, target := range journalTargets {
					if old, ok := targets[key]; ok && old.AppID != target.AppID {
						item.ResourceEvidenceComplete = false
						item.Blockers = append(item.Blockers, "Receipt and journal app identities conflict.")
					}
					targets[key] = target
				}
			}
		}
		evidence[batch.ID] = source
		// Preserve recorded fixture/consumer/workload cleanup failures even when the
		// workload session itself is now absent.
		reportPath := filepath.Join(directory, "batch-"+batch.ID+".json")
		if roots, hash, err := readTestBaselineReport(reportPath, nil); err == nil {
			if saved.ReportSHA256 != "" && hash != saved.ReportSHA256 {
				item.ResourceEvidenceComplete = false
				item.Blockers = append(item.Blockers, "Finalized report differs from its checkpoint.")
			}
			for _, root := range roots {
				if testCampaignReceiptCleanupFailed(root, 0) {
					item.RecordedCleanupFailure = true
				}
			}
		} else if saved.Status == "done" {
			item.ResourceEvidenceComplete = false
			item.Blockers = append(item.Blockers, "Finalized report is missing or unreadable.")
		}
		keys := make([]string, 0, len(targets))
		for key := range targets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			target := targets[key]
			if owner, ok := seenRuns[target.RunID]; ok && owner != batch.ID {
				item.ResourceEvidenceComplete = false
				item.Blockers = append(item.Blockers, "Native run identity appears in more than one saved batch.")
			}
			seenRuns[target.RunID] = batch.ID
			resource := testCampaignRecoveryResource{Project: target.Project, RunID: target.RunID, AppID: target.AppID, Live: "not_checked"}
			if reader != nil {
				if report.LiveLookups >= 1024 {
					resource.Live = "limit_reached"
				} else {
					report.LiveLookups++
					session, err := reader.GetDevSession(ctx, target.Project, target.RunID)
					if isNotFound(err) {
						resource.Live = "absent"
					} else if err != nil {
						resource.Live = "unknown"
					} else if session.App.ID != target.AppID {
						resource.Live = "identity_changed"
					} else {
						resource.Live = "present"
					}
				}
			}
			if resource.Live != "absent" {
				item.KnownSessionsAbsent = false
			}
			item.Resources = append(item.Resources, resource)
		}
		if !item.ResourceEvidenceComplete {
			report.ResourceEvidenceComplete = false
			item.Blockers = append(item.Blockers, "Resource inventory is not fully verified.")
		}
		if !item.KnownSessionsAbsent {
			report.KnownSessionsAbsent = false
			item.Blockers = append(item.Blockers, "Known workload session absence is not verified.")
		}
		if !complete {
			item.Blockers = append(item.Blockers, "Verified terminal execution evidence is incomplete; cleanup grants no test completion.")
		}
		if item.RecordedCleanupFailure {
			item.Blockers = append(item.Blockers, "Recorded fixture, consumer or workload cleanup errors remain blocking and require manual reconciliation.")
		}
		for _, reason := range item.Blockers {
			report.Blockers = append(report.Blockers, batch.ID+": "+reason)
		}
		report.Batches = append(report.Batches, item)
	}
	duplicates := map[string]bool{}
	for _, path := range paths {
		link := linkTestCampaignCleanupObservation(path, plan, cp, evidence)
		if link.SHA256 != "" && duplicates[link.SHA256] {
			link.Status = "duplicate"
			link.OperationComplete = false
		}
		duplicates[link.SHA256] = true
		if link.Status == "linked" {
			report.LinkedObservations++
		}
		report.Observations = append(report.Observations, link)
	}
	authorized := assessment.Ownership == "local" || assessment.Ownership == "active_recipient"
	if checkTestCampaignBundleWork(directory, resolver, true) != nil {
		authorized = false
	}
	if resolver != nil {
		if resolver.requireActive() != nil {
			authorized = false
		}
		if _, err := os.Lstat(resolver.Index.ClaimPath + ".lock"); !os.IsNotExist(err) {
			authorized = false
		}
	}
	if _, err := os.Lstat(filepath.Join(directory, testCampaignContinuationOwnerName)); !os.IsNotExist(err) {
		authorized = false
	}
	for _, parent := range assessment.Status.Lineage {
		if !parent.Verified {
			authorized = false
		}
	}
	if !authorized {
		report.Blockers = append(report.Blockers, "Current ownership is unavailable; use the active recipient's evidence.")
	}
	if assessment.Status.LockPresent {
		report.Blockers = append(report.Blockers, "Runner lock is present; completion cannot be certified while work may be active.")
	}
	if !report.SnapshotStable {
		report.Blockers = append(report.Blockers, "Evidence changed during inspection; repeat verification.")
	}
	if !report.TestExecutionComplete {
		report.Blockers = append(report.Blockers, "Saved plan still lacks verified execution evidence for every selected batch; pending work and missing reports remain incomplete.")
	}
	if !report.ResourceEvidenceComplete {
		report.Blockers = append(report.Blockers, "The full workload resource inventory is unresolved.")
	}
	if !report.KnownSessionsAbsent {
		report.Blockers = append(report.Blockers, "Current absence of all known workload sessions has not been verified.")
	}
	if !report.PolicyPassed {
		report.Blockers = append(report.Blockers, "Campaign policy remains unsatisfied.")
	}
	for _, blocker := range assessment.Status.Issues {
		if blocker.Code == "evidence_conflict" || blocker.Code == "snapshot_changed" {
			report.ResourceEvidenceComplete = false
			report.Blockers = append(report.Blockers, "Conflicting or changing source evidence prevents completion verification.")
		}
	}
	report.RecoveryComplete = report.ResourceEvidenceComplete && report.KnownSessionsAbsent && report.TestExecutionComplete &&
		report.PolicyPassed && report.SnapshotStable && !assessment.Status.LockPresent && authorized
	for _, batch := range report.Batches {
		if batch.RecordedCleanupFailure {
			report.RecoveryComplete = false
		}
	}
	return report
}

func invalidateTestCampaignRecoveryCompletion(report *testCampaignRecoveryCompletion) {
	if report == nil {
		return
	}
	report.SnapshotStable = false
	report.RecoveryComplete = false
	report.KnownSessionsAbsent = false
	for i := range report.Batches {
		report.Batches[i].KnownSessionsAbsent = false
	}
	report.Blockers = append(report.Blockers, "Evidence or ownership changed while inspected; repeat completion verification.")
}
