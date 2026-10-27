from typing import Literal

OperationWorkflowConditionRefreshProgressChangeStatus = Literal[
    "budget_removed", "contract_changed", "refreshed", "still_overdue", "unreported"
]

OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_CHANGE_STATUS_VALUES: set[
    OperationWorkflowConditionRefreshProgressChangeStatus
] = {
    "budget_removed",
    "contract_changed",
    "refreshed",
    "still_overdue",
    "unreported",
}


def check_operation_workflow_condition_refresh_progress_change_status(
    value: str,
) -> OperationWorkflowConditionRefreshProgressChangeStatus:
    if value in OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_REFRESH_PROGRESS_CHANGE_STATUS_VALUES!r}"
    )
