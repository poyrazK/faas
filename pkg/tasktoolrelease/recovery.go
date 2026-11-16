package tasktoolrelease

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type RecoveryCheck struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Code  string `json:"code"`
}

type RecoveryDeployment struct {
	Role            string `json:"role"`
	DeploymentID    string `json:"deployment_id,omitempty"`
	Lookup          string `json:"lookup"`
	Status          string `json:"status"`
	Matches         int    `json:"matches"`
	HistoryComplete bool   `json:"history_complete"`
}

type RecoveryAction struct {
	Action              string   `json:"action"`
	Eligibility         string   `json:"eligibility"`
	Blockers            []string `json:"blockers"`
	RequiredOwnerChecks []string `json:"required_owner_checks"`
}

// RecoveryReport is a bounded, read-only observation and optionally a plan.
// Hashes bind this report's bytes; no apply endpoint consumes them as authority.
type RecoveryReport struct {
	Version                    int                         `json:"version"`
	Kind                       string                      `json:"kind"`
	IntentID                   string                      `json:"intent_id"`
	AccountID                  string                      `json:"account_id"`
	OperationID                string                      `json:"operation_id,omitempty"`
	IntentStatus               string                      `json:"intent_status"`
	ExecutionState             string                      `json:"execution_state"`
	Classification             string                      `json:"classification"`
	Receipt                    *api.TaskToolReleaseReceipt `json:"receipt,omitempty"`
	CompletedStages            []string                    `json:"completed_stages"`
	ObservedAt                 time.Time                   `json:"observed_at"`
	ExpiresAt                  time.Time                   `json:"expires_at"`
	Checks                     []RecoveryCheck             `json:"checks"`
	Deployments                []RecoveryDeployment        `json:"deployments"`
	Actions                    []RecoveryAction            `json:"actions,omitempty"`
	EvidenceSHA256             string                      `json:"evidence_sha256"`
	RecoveryPlanID             string                      `json:"recovery_plan_id,omitempty"`
	ReadOnly                   bool                        `json:"read_only"`
	ReadyToApply               bool                        `json:"ready_to_apply"`
	OwnerAuthorizationRequired bool                        `json:"owner_authorization_required"`
}

func recoveryFact(report *RecoveryReport, name, state, code string) {
	report.Checks = append(report.Checks, RecoveryCheck{Name: name, State: state, Code: code})
}
func recoveryPassed(report RecoveryReport, name string) bool {
	for _, fact := range report.Checks {
		if fact.Name == name {
			return fact.State == "passed"
		}
	}
	return false
}
func roleFact(app api.TaskToolReleaseApp, name string) string {
	return app.Role + "/" + app.App + "/" + name
}

// InspectRecovery only calls native GET APIs. It never loads local files,
// credentials, customer code, Task databases or public application endpoints.
func InspectRecovery(ctx context.Context, c *api.Client, id string, plan bool, now time.Time) (RecoveryReport, error) {
	ctx, cancel := context.WithTimeout(ctx, api.TaskToolRecoveryObservationTTL)
	defer cancel()
	var out RecoveryReport
	if !toolpublication.ValidID(id) {
		return out, ErrInvalid
	}
	record, err := c.GetTaskToolReleaseIntent(ctx, id)
	if err != nil {
		return out, err
	}
	expected, err := New(record.AccountID, record.RequesterKeyID, id, record.Intent, record.CreatedAt)
	if err != nil || record.ID != id || record.Version != 1 || record.CreatedAt.IsZero() || record.IntentSHA256 != expected.IntentSHA256 || !record.ExpiresAt.Equal(expected.ExpiresAt) || record.ReadyToApply || record.BindingsVerified || !slices.Contains([]string{"pending", "approved", "rejected", "revoked", "expired", "stale"}, record.Status) {
		return out, ErrInvalid
	}
	out = RecoveryReport{Version: 1, Kind: "inspection", IntentID: id, AccountID: record.AccountID, IntentStatus: View(record, now).Status,
		ExecutionState: "none", Classification: "unknown", ObservedAt: now.UTC(), ExpiresAt: now.UTC().Add(api.TaskToolRecoveryObservationTTL),
		CompletedStages: []string{}, Checks: []RecoveryCheck{}, Deployments: []RecoveryDeployment{}, ReadOnly: true, OwnerAuthorizationRequired: true}
	if record.Execution == nil {
		recoveryFact(&out, "receipt", "unknown", "execution_not_recorded")
		return finishRecovery(out, record, plan), nil
	}
	e := record.Execution
	if !toolpublication.ValidID(e.OperationID) || !toolpublication.ValidID(e.BindingsSHA256) || !slices.Contains([]string{"authorized", "running", "reconcile_required", "completed"}, e.State) {
		return out, ErrInvalid
	}
	out.OperationID, out.ExecutionState = e.OperationID, e.State
	if e.Receipt == nil {
		recoveryFact(&out, "receipt", "unknown", "checkpoint_missing")
		return finishRecovery(out, record, plan), nil
	}
	if ValidateReceipt(record, *e.Receipt) != nil {
		return out, ErrInvalid
	}
	copyBytes, _ := json.Marshal(e.Receipt)
	_ = json.Unmarshal(copyBytes, &out.Receipt)
	out.CompletedStages = ReceiptStages(*out.Receipt)
	recoveryFact(&out, "receipt", "passed", "checkpoint_recorded")
	for _, role := range []string{"worker", "web", "baseline"} {
		deployment := observeRecoveryDeployment(ctx, c, record, role)
		out.Deployments = append(out.Deployments, deployment)
		state, code := "unknown", "deployment_unavailable"
		if deployment.Lookup == "not_submitted" {
			code = "not_submitted"
		} else if deployment.Lookup == "ambiguous_history" || deployment.Lookup == "identity_mismatch" {
			state, code = "failed", deployment.Lookup
		} else if deployment.Status == "live" {
			state, code = "passed", "native_deployment_live"
		} else if deployment.Status == "failed" {
			state, code = "failed", "native_deployment_failed"
		} else if deployment.Status == "pending" {
			code = "native_deployment_pending"
		}
		recoveryFact(&out, "deployment/"+role, state, code)
		if e.Receipt.PendingSubmission == role {
			state, code = "unknown", "submission_unresolved"
			if deployment.Lookup == "unique_history_match" && deployment.HistoryComplete {
				state, code = "passed", "submission_found_once"
			}
			if deployment.Lookup == "ambiguous_history" || deployment.Lookup == "identity_mismatch" {
				state, code = "failed", deployment.Lookup
			}
			recoveryFact(&out, "submission/"+role, state, code)
		}
	}
	for _, app := range record.Intent.Apps {
		observeRecoveryApp(ctx, c, record, app, &out)
	}
	observeRecoveryObserver(ctx, c, record, &out, now)
	latest, err := c.GetTaskToolReleaseIntent(ctx, id)
	if err != nil {
		recoveryFact(&out, "journal_stable", "unknown", "journal_unavailable")
	} else {
		before, _ := json.Marshal(record)
		after, _ := json.Marshal(latest)
		if string(before) != string(after) {
			recoveryFact(&out, "journal_stable", "unknown", "journal_changed_during_inspection")
		} else {
			recoveryFact(&out, "journal_stable", "passed", "journal_unchanged")
		}
	}
	return finishRecovery(out, record, plan), nil
}

func finishRecovery(out RecoveryReport, record api.TaskToolReleaseIntentRecord, plan bool) RecoveryReport {
	if out.Receipt != nil {
		switch out.ExecutionState {
		case "authorized":
			out.Classification = "not_started"
		case "running":
			out.Classification = "running_or_interrupted"
		case "reconcile_required":
			out.Classification = "owner_reconciliation_required"
		case "completed":
			out.Classification = "completion_recorded"
		}
	}
	if plan {
		out.Kind = "recovery_plan"
		out.Actions = recoveryActions(out, record)
	}
	b, _ := json.Marshal(out)
	out.EvidenceSHA256 = toolpublication.Digest(b)
	if plan {
		out.RecoveryPlanID = toolpublication.Digest([]byte("task-recovery-plan:" + out.EvidenceSHA256))
	}
	return out
}

func recoveryActions(report RecoveryReport, record api.TaskToolReleaseIntentRecord) []RecoveryAction {
	common := []string{}
	if !recoveryPassed(report, "receipt") {
		common = append(common, "checkpoint_missing")
	}
	if !recoveryPassed(report, "journal_stable") {
		common = append(common, "journal_not_stable")
	}
	if report.ExecutionState != "running" && report.ExecutionState != "reconcile_required" {
		common = append(common, "operation_not_interrupted")
	}
	if report.Receipt != nil && report.Receipt.PendingSubmission != "" && !recoveryPassed(report, "submission/"+report.Receipt.PendingSubmission) {
		common = append(common, "submission_unresolved")
	}
	for _, app := range record.Intent.Apps {
		for _, fact := range []string{"identity", "configuration"} {
			if !recoveryPassed(report, roleFact(app, fact)) {
				common = append(common, roleFact(app, fact))
			}
		}
	}
	out := []RecoveryAction{}
	for _, action := range []string{"resume", "restore", "retire"} {
		blockers := slices.Clone(common)
		if !recoveryPassed(report, "observer_fresh") {
			blockers = append(blockers, "observer_not_verified")
		}
		switch action {
		case "resume":
			if !recoveryPassed(report, "web_traffic_expected") {
				blockers = append(blockers, "serving_generation_changed_or_unknown")
			}
			if report.Receipt != nil && report.Receipt.Promoted && !recoveryPassed(report, "web_candidate_serving") {
				blockers = append(blockers, "recorded_promotion_no_longer_serving")
			}
			for _, dep := range report.Deployments {
				if dep.Role != "baseline" && dep.Lookup != "not_submitted" && dep.Status != "live" {
					blockers = append(blockers, "candidate_not_live/"+dep.Role)
				}
			}
		case "restore":
			if !recoveryPassed(report, "web_candidate_serving") || !recoveryPassed(report, "deployment/baseline") {
				blockers = append(blockers, "captured_baseline_or_candidate_traffic_unavailable")
			}
		case "retire":
			if !recoveryPassed(report, "web_baseline_serving") || !recoveryPassed(report, "deployment/baseline") {
				blockers = append(blockers, "baseline_not_restored")
			}
			if report.Receipt == nil || len(report.Receipt.WorkerIDs) == 0 || !recoveryPassed(report, "deployment/worker") {
				blockers = append(blockers, "candidate_worker_receipts_unavailable")
			}
		}
		for _, app := range record.Intent.Apps {
			if app.Role == "previous_worker" {
				if !recoveryPassed(report, roleFact(app, "generation")) {
					blockers = append(blockers, roleFact(app, "generation"))
				}
				parked := report.Receipt != nil && report.Receipt.Parked[app.App]
				if action != "resume" && parked {
					blockers = append(blockers, "previous_worker_already_parked/"+app.App)
				}
				parkObserved := action == "resume" && recoveryPassed(report, "web_candidate_serving") && recoveryPassed(report, roleFact(app, "zero_live"))
				if !parked && !parkObserved && !recoveryPassed(report, roleFact(app, "instances")) {
					blockers = append(blockers, roleFact(app, "instances"))
				}
			}
			if app.Role == "worker" && recoveryDeploymentID(report, "worker") != "" && !recoveryPassed(report, roleFact(app, "generation")) {
				blockers = append(blockers, roleFact(app, "generation"))
			}
			if app.Role == "worker" && recoveryDeploymentID(report, "worker") != "" {
				if action == "resume" && !recoveryPassed(report, roleFact(app, "instances")) {
					blockers = append(blockers, roleFact(app, "instances"))
				}
				if action == "retire" && !recoveryPassed(report, roleFact(app, "instances")) && !recoveryPassed(report, roleFact(app, "zero_live")) {
					blockers = append(blockers, roleFact(app, "instances"))
				}
			}
		}
		eligibility := "requires_owner_checks"
		if len(blockers) > 0 {
			eligibility = "blocked"
		}
		out = append(out, RecoveryAction{Action: action, Eligibility: eligibility, Blockers: blockers, RequiredOwnerChecks: []string{
			"original_artifacts_and_installed_dependencies", "database_bindings_retained_keys_and_task_compatibility", "live_endpoint_catalog_and_role_readiness", "fresh_native_traffic_and_worker_generation_fences", "separate_owner_recovery_authorization"}})
	}
	return out
}

func recoveryDeploymentID(report RecoveryReport, role string) string {
	for _, dep := range report.Deployments {
		if dep.Role == role {
			return dep.DeploymentID
		}
	}
	return ""
}

func recoveryAppPin(record api.TaskToolReleaseIntentRecord, role string) (api.TaskToolReleaseApp, bool) {
	for _, app := range record.Intent.Apps {
		if app.Role == role {
			return app, true
		}
	}
	return api.TaskToolReleaseApp{}, false
}
