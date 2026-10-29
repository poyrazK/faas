from typing import Literal

ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus = Literal["no_budget", "overdue", "within_budget"]

LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES: set[
    ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus
] = {
    "no_budget",
    "overdue",
    "within_budget",
}


def check_list_account_workflow_performance_instances_condition_evidence_status(
    value: str,
) -> ListAccountWorkflowPerformanceInstancesConditionEvidenceStatus:
    if value in LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {LIST_ACCOUNT_WORKFLOW_PERFORMANCE_INSTANCES_CONDITION_EVIDENCE_STATUS_VALUES!r}"
    )
