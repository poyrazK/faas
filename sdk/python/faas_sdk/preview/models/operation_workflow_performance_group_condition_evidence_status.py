from typing import Literal

OperationWorkflowPerformanceGroupConditionEvidenceStatus = Literal["no_budget", "overdue", "within_budget"]

OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_EVIDENCE_STATUS_VALUES: set[
    OperationWorkflowPerformanceGroupConditionEvidenceStatus
] = {
    "no_budget",
    "overdue",
    "within_budget",
}


def check_operation_workflow_performance_group_condition_evidence_status(
    value: str,
) -> OperationWorkflowPerformanceGroupConditionEvidenceStatus:
    if value in OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_EVIDENCE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_PERFORMANCE_GROUP_CONDITION_EVIDENCE_STATUS_VALUES!r}"
    )
