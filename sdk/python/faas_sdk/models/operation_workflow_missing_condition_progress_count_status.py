from typing import Literal

OperationWorkflowMissingConditionProgressCountStatus = Literal[
    "contract_changed", "expectation_changed", "not_expected", "received_report", "still_missing"
]

OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES: set[
    OperationWorkflowMissingConditionProgressCountStatus
] = {
    "contract_changed",
    "expectation_changed",
    "not_expected",
    "received_report",
    "still_missing",
}


def check_operation_workflow_missing_condition_progress_count_status(
    value: str,
) -> OperationWorkflowMissingConditionProgressCountStatus:
    if value in OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES!r}"
    )
