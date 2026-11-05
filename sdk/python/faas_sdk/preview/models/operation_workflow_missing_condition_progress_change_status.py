from typing import Literal

OperationWorkflowMissingConditionProgressChangeStatus = Literal[
    "contract_changed", "expectation_changed", "not_expected", "received_report", "still_missing"
]

OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES: set[
    OperationWorkflowMissingConditionProgressChangeStatus
] = {
    "contract_changed",
    "expectation_changed",
    "not_expected",
    "received_report",
    "still_missing",
}


def check_operation_workflow_missing_condition_progress_change_status(
    value: str,
) -> OperationWorkflowMissingConditionProgressChangeStatus:
    if value in OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES!r}"
    )
