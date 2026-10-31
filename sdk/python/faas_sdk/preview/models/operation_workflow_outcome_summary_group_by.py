from typing import Literal

OperationWorkflowOutcomeSummaryGroupBy = Literal["customer", "outcome", "workflow"]

OPERATION_WORKFLOW_OUTCOME_SUMMARY_GROUP_BY_VALUES: set[OperationWorkflowOutcomeSummaryGroupBy] = {
    "customer",
    "outcome",
    "workflow",
}


def check_operation_workflow_outcome_summary_group_by(value: str) -> OperationWorkflowOutcomeSummaryGroupBy:
    if value in OPERATION_WORKFLOW_OUTCOME_SUMMARY_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OUTCOME_SUMMARY_GROUP_BY_VALUES!r}"
    )
