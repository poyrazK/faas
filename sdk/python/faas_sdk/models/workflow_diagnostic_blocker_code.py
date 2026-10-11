from typing import Literal

WorkflowDiagnosticBlockerCode = Literal[
    "account_inactive",
    "active_attempt",
    "active_run_quota",
    "active_step",
    "app_deleted",
    "cancelled",
    "deployment_unavailable",
    "failed_control_step",
    "failure_before_dispatch",
    "handler_executed",
    "incomplete_step_state",
    "integration_unavailable",
    "invalid_definition",
    "maintenance",
    "no_failed_actions",
    "pinned_deployment_unavailable",
    "plan_not_allowed",
    "resume_limit_reached",
    "run_not_failed",
    "runtime_disabled",
    "tenant_required",
    "tenant_unavailable",
    "unsafe_mutation",
]

WORKFLOW_DIAGNOSTIC_BLOCKER_CODE_VALUES: set[WorkflowDiagnosticBlockerCode] = {
    "account_inactive",
    "active_attempt",
    "active_run_quota",
    "active_step",
    "app_deleted",
    "cancelled",
    "deployment_unavailable",
    "failed_control_step",
    "failure_before_dispatch",
    "handler_executed",
    "incomplete_step_state",
    "integration_unavailable",
    "invalid_definition",
    "maintenance",
    "no_failed_actions",
    "pinned_deployment_unavailable",
    "plan_not_allowed",
    "resume_limit_reached",
    "run_not_failed",
    "runtime_disabled",
    "tenant_required",
    "tenant_unavailable",
    "unsafe_mutation",
}


def check_workflow_diagnostic_blocker_code(value: str) -> WorkflowDiagnosticBlockerCode:
    if value in WORKFLOW_DIAGNOSTIC_BLOCKER_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_DIAGNOSTIC_BLOCKER_CODE_VALUES!r}")
