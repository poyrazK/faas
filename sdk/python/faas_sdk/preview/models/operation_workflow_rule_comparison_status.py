from typing import Literal

OperationWorkflowRuleComparisonStatus = Literal["compared", "insufficient_data"]

OPERATION_WORKFLOW_RULE_COMPARISON_STATUS_VALUES: set[OperationWorkflowRuleComparisonStatus] = {
    "compared",
    "insufficient_data",
}


def check_operation_workflow_rule_comparison_status(value: str) -> OperationWorkflowRuleComparisonStatus:
    if value in OPERATION_WORKFLOW_RULE_COMPARISON_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RULE_COMPARISON_STATUS_VALUES!r}")
