from typing import Literal

OperationWorkflowConditionFreshnessStatus = Literal["no_budget", "overdue", "within_budget"]

OPERATION_WORKFLOW_CONDITION_FRESHNESS_STATUS_VALUES: set[OperationWorkflowConditionFreshnessStatus] = {
    "no_budget",
    "overdue",
    "within_budget",
}


def check_operation_workflow_condition_freshness_status(value: str) -> OperationWorkflowConditionFreshnessStatus:
    if value in OPERATION_WORKFLOW_CONDITION_FRESHNESS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_CONDITION_FRESHNESS_STATUS_VALUES!r}"
    )
