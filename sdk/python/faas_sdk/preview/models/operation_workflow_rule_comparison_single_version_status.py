from typing import Literal

OperationWorkflowRuleComparisonSingleVersionStatus = Literal["compared", "insufficient_data"]

OPERATION_WORKFLOW_RULE_COMPARISON_SINGLE_VERSION_STATUS_VALUES: set[
    OperationWorkflowRuleComparisonSingleVersionStatus
] = {
    "compared",
    "insufficient_data",
}


def check_operation_workflow_rule_comparison_single_version_status(
    value: str,
) -> OperationWorkflowRuleComparisonSingleVersionStatus:
    if value in OPERATION_WORKFLOW_RULE_COMPARISON_SINGLE_VERSION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_RULE_COMPARISON_SINGLE_VERSION_STATUS_VALUES!r}"
    )
