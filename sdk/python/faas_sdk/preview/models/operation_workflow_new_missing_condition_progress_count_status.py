from typing import Literal

OperationWorkflowNewMissingConditionProgressCountStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES: set[
    OperationWorkflowNewMissingConditionProgressCountStatus
] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_operation_workflow_new_missing_condition_progress_count_status(
    value: str,
) -> OperationWorkflowNewMissingConditionProgressCountStatus:
    if value in OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_COUNT_STATUS_VALUES!r}"
    )
