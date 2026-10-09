from typing import Literal

OperationWorkflowAttentionSummaryGroupBy = Literal[
    "blocker_code", "customer", "dependency_status", "required_outcome_code", "target_operation", "workflow"
]

OPERATION_WORKFLOW_ATTENTION_SUMMARY_GROUP_BY_VALUES: set[OperationWorkflowAttentionSummaryGroupBy] = {
    "blocker_code",
    "customer",
    "dependency_status",
    "required_outcome_code",
    "target_operation",
    "workflow",
}


def check_operation_workflow_attention_summary_group_by(value: str) -> OperationWorkflowAttentionSummaryGroupBy:
    if value in OPERATION_WORKFLOW_ATTENTION_SUMMARY_GROUP_BY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_ATTENTION_SUMMARY_GROUP_BY_VALUES!r}"
    )
