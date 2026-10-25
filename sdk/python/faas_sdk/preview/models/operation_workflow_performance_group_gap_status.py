from typing import Literal

OperationWorkflowPerformanceGroupGapStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_GAP_STATUS_VALUES: set[OperationWorkflowPerformanceGroupGapStatus] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_operation_workflow_performance_group_gap_status(value: str) -> OperationWorkflowPerformanceGroupGapStatus:
    if value in OPERATION_WORKFLOW_PERFORMANCE_GROUP_GAP_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_GROUP_GAP_STATUS_VALUES!r}"
    )
