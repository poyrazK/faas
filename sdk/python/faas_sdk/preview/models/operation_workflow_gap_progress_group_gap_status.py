from typing import Literal

OperationWorkflowGapProgressGroupGapStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

OPERATION_WORKFLOW_GAP_PROGRESS_GROUP_GAP_STATUS_VALUES: set[OperationWorkflowGapProgressGroupGapStatus] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_operation_workflow_gap_progress_group_gap_status(value: str) -> OperationWorkflowGapProgressGroupGapStatus:
    if value in OPERATION_WORKFLOW_GAP_PROGRESS_GROUP_GAP_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_GAP_PROGRESS_GROUP_GAP_STATUS_VALUES!r}"
    )
