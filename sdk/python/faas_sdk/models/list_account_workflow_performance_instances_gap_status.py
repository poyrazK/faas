from typing import Literal

ListAccountWorkflowPerformanceInstancesGapStatus = Literal[
    "contract_changed", "expectation_changed", "new_expectation", "report_lost", "still_missing"
]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES: set[ListAccountWorkflowPerformanceInstancesGapStatus] = {
    "contract_changed",
    "expectation_changed",
    "new_expectation",
    "report_lost",
    "still_missing",
}


def check_list_account_workflow_performance_instances_gap_status(
    value: str,
) -> ListAccountWorkflowPerformanceInstancesGapStatus:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_GAP_STATUS_VALUES!r}"
    )
