from typing import Literal

OperationWorkflowRevisionComparisonStatus = Literal[
    "ambiguous_revision", "baseline_unavailable", "compared", "contract_changed", "revision_gap"
]

OPERATION_WORKFLOW_REVISION_COMPARISON_STATUS_VALUES: set[OperationWorkflowRevisionComparisonStatus] = {
    "ambiguous_revision",
    "baseline_unavailable",
    "compared",
    "contract_changed",
    "revision_gap",
}


def check_operation_workflow_revision_comparison_status(value: str) -> OperationWorkflowRevisionComparisonStatus:
    if value in OPERATION_WORKFLOW_REVISION_COMPARISON_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_REVISION_COMPARISON_STATUS_VALUES!r}"
    )
