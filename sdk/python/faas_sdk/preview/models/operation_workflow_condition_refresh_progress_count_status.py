from typing import Literal

OperationWorkflowConditionRefreshProgressCountStatus = Literal[
    "budget_removed", "contract_changed", "refreshed", "still_overdue", "unreported"
]

OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_COUNT_STATUS_VALUES: set[
    OperationWorkflowConditionRefreshProgressCountStatus
] = {
    "budget_removed",
    "contract_changed",
    "refreshed",
    "still_overdue",
    "unreported",
}


def check_operation_workflow_condition_refresh_progress_count_status(
    value: str,
) -> OperationWorkflowConditionRefreshProgressCountStatus:
    if value in OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_COUNT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_COUNT_STATUS_VALUES!r}"
    )
