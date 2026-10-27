from typing import Literal

OperationWorkflowConditionComparisonStatus = Literal[
    "ambiguous_revision", "baseline_unavailable", "compared", "contract_changed", "revision_gap"
]

OPERATION_WORKFLOW_CONDITION_COMPARISON_STATUS_VALUES: set[OperationWorkflowConditionComparisonStatus] = {
    "ambiguous_revision",
    "baseline_unavailable",
    "compared",
    "contract_changed",
    "revision_gap",
}


def check_operation_workflow_condition_comparison_status(value: str) -> OperationWorkflowConditionComparisonStatus:
    if value in OPERATION_WORKFLOW_CONDITION_COMPARISON_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_COMPARISON_STATUS_VALUES!r}"
    )
