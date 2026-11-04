from typing import Literal

OperationWorkflowNewMissingConditionProgressChangeStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES: set[
    OperationWorkflowNewMissingConditionProgressChangeStatus
] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_operation_workflow_new_missing_condition_progress_change_status(
    value: str,
) -> OperationWorkflowNewMissingConditionProgressChangeStatus:
    if value in OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEW_MISSING_CONDITION_PROGRESS_CHANGE_STATUS_VALUES!r}"
    )
