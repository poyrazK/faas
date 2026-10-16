package diagnostics

import "github.com/onebox-faas/faas/pkg/api"

// Explain never echoes arbitrary upstream error text or suggests enabling an
// execution gate. Unknown reason codes remain explicit, with operator review.
func Explain(resource, code string) api.EnvironmentGitOpsDiagnostic {
	issue := api.EnvironmentGitOpsDiagnostic{Resource: resource, Stage: "assessment", Code: code, Actor: "operator"}
	switch code {
	case "source_binding_unknown":
		issue.Message, issue.Action = "The source binding is unknown.", "Read the current environment binding before reviewing a revision."
	case "source_suspended", "source_detached":
		issue.Stage, issue.Actor = "source", "customer"
		issue.Message, issue.Action = "The source is suspended or detached.", "Review the source controls and retained workloads before changing the binding."
	case "source_stale", "source_unknown":
		issue.Stage = "source"
		issue.Message, issue.Action = "A recent successful Git check is not proven.", "Inspect source polling and provider access; keep the last approved revision authoritative."
	case "environment_git_source_unavailable", "environment_git_repository_unavailable", "environment_gitlab_source_unavailable", "environment_git_approval_unavailable":
		issue.Stage = "source"
		issue.Message, issue.Action = "Git source or merge evidence could not be verified.", "Check provider connectivity and the account connection; approval alone does not replace verification."
	case "approval_missing", "environment_git_approval_not_qualified":
		issue.Stage, issue.Actor = "approval", "customer"
		issue.Message, issue.Action = "The revision lacks the required immutable approval.", "Review the intended commit and its plan, then use the configured approval policy."
	case "report_mode":
		issue.Stage, issue.Actor = "mode", "customer"
		issue.Message, issue.Action = "This source reports drift without applying it.", "Review the reported plan. Production execution remains separately gated; do not enable it from this assessment."
	case "current_graph_missing", "current_graph_stale":
		issue.Stage = "preparation"
		issue.Message, issue.Action = "No matching current workload graph is available.", "Inspect the current reconciliation attempt and ownership plan; historical applied revisions do not prove serving."
	case "environment_artifact_preparation_incomplete":
		issue.Stage, issue.Actor = "preparation", "controller"
		issue.Message, issue.Action = "The reviewed artifact cohort is not fully prepared.", "Inspect the candidate build or image preparation result; wait for recorded completion before qualification."
	case "environment_workload_build_failed":
		issue.Stage, issue.Actor = "preparation", "customer"
		issue.Message, issue.Action = "The candidate build or deployment failed or was cancelled.", "Inspect build diagnostics and review a corrected commit or an explicitly approved rollback."
	case "environment_capture_evidence_missing", "environment_restore_evidence_missing", "environment_guest_config_acknowledgement_missing", "environment_framework_ready_evidence_missing", "environment_isolated_smoke_evidence_missing", "environment_job_execution_smoke_evidence_missing":
		issue.Stage = "qualification"
		issue.Message, issue.Action = "Current attempt-bound runtime evidence is incomplete.", "Inspect qualification attempts and native acceptance availability. Keep production gates closed until their required acceptance passes."
	case "environment_activation_evidence_missing":
		issue.Stage, issue.Actor = "activation", "controller"
		issue.Message, issue.Action = "The current graph has no complete active release-set match.", "Inspect qualification and release publication; do not promote individual held candidates."
	case "environment_serving_evidence_missing":
		issue.Stage, issue.Actor = "serving", "controller"
		issue.Message, issue.Action = "Current serving convergence has not been proven.", "Inspect route, queue and scheduled occurrence acknowledgements for this exact release."
	case "scheduled_occurrence_unproven":
		issue.Stage, issue.Actor = "serving", "customer"
		issue.Message, issue.Action = "The qualified, activated Job lacks current graph serving proof.", "Inspect the current schedule occurrence and task logs; a successful build or smoke is not a scheduled-run receipt."
	case "environment_scheduled_job_materialization_unready":
		issue.Stage, issue.Actor = "preparation", "controller"
		issue.Message, issue.Action = "A managed schedule is not bound to its current qualified artifact.", "Inspect schedule materialization and the reviewed release; scheduled runs must not rebuild or borrow another artifact."
	case "environment_retained_workload_evidence_missing":
		issue.Stage = "activation"
		issue.Message, issue.Action = "A retained workload no longer matches its active deployment.", "Inspect retained workload identity and release membership; use reviewed recovery rather than replacing it."
	case "environment_workload_production_execution_adapter_missing", "environment_job_queue_binding_execution_unsupported", "environment_queue_binding_execution_unsupported", "environment_service_binding_contract_unsupported", "environment_job_production_adapter_missing", "environment_scheduled_job_dispatch_adapter_missing", "environment_workload_execution_mode_unsupported", "environment_queue_binding_mode_unsupported":
		issue.Stage, issue.Actor = "contract", "customer"
		issue.Message, issue.Action = "The reviewed graph needs an unsupported execution contract or adapter.", "Review the supported workload contract and correct the definition; retrying or enabling a gate cannot add adapter support."
	case "environment_job_execution_smoke_contract_missing", "environment_worker_queue_smoke_contract_missing", "environment_function_queue_smoke_evidence_missing":
		issue.Stage, issue.Actor = "qualification", "customer"
		issue.Message, issue.Action = "Required reviewed smoke configuration or evidence is missing.", "Review the workload smoke contract and inspect its exact qualification attempt."
	case "environment_activation_before_qualification", "environment_workload_graph_empty":
		issue.Message, issue.Action = "The recorded graph does not establish a valid qualification order.", "Ask the operator to inspect graph consistency; do not infer readiness from empty or out-of-order evidence."
	default:
		issue.Code = "unrecognized_blocker"
		issue.Message, issue.Action = "A blocker has no recognized diagnostic mapping.", "Ask the operator to inspect the current reconciliation attempt; this assessment cannot safely suggest a repair."
	}
	return issue
}
