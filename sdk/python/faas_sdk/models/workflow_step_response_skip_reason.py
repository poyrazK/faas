from typing import Literal

WorkflowStepResponseSkipReason = Literal["dependency_failed", "dependency_skipped", "route_not_taken", "when_false"]

WORKFLOW_STEP_RESPONSE_SKIP_REASON_VALUES: set[WorkflowStepResponseSkipReason] = {
    "dependency_failed",
    "dependency_skipped",
    "route_not_taken",
    "when_false",
}


def check_workflow_step_response_skip_reason(value: str) -> WorkflowStepResponseSkipReason:
    if value in WORKFLOW_STEP_RESPONSE_SKIP_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WORKFLOW_STEP_RESPONSE_SKIP_REASON_VALUES!r}")
