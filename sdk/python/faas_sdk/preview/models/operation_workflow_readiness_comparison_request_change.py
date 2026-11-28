from typing import Literal

OperationWorkflowReadinessComparisonRequestChange = Literal[
    "changed", "newly_blocked", "newly_ready", "unavailable", "unchanged"
]

OPERATION_WORKFLOW_READINESS_COMPARISON_REQUEST_CHANGE_VALUES: set[
    OperationWorkflowReadinessComparisonRequestChange
] = {
    "changed",
    "newly_blocked",
    "newly_ready",
    "unavailable",
    "unchanged",
}


def check_operation_workflow_readiness_comparison_request_change(
    value: str,
) -> OperationWorkflowReadinessComparisonRequestChange:
    if value in OPERATION_WORKFLOW_READINESS_COMPARISON_REQUEST_CHANGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_READINESS_COMPARISON_REQUEST_CHANGE_VALUES!r}"
    )
