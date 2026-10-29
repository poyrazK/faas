from typing import Literal

OperationWorkflowNextActionDigestExplanationComparison = Literal[
    "first_delivery", "previous_delivery", "previous_snapshot_unavailable"
]

OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_COMPARISON_VALUES: set[
    OperationWorkflowNextActionDigestExplanationComparison
] = {
    "first_delivery",
    "previous_delivery",
    "previous_snapshot_unavailable",
}


def check_operation_workflow_next_action_digest_explanation_comparison(
    value: str,
) -> OperationWorkflowNextActionDigestExplanationComparison:
    if value in OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_COMPARISON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_COMPARISON_VALUES!r}"
    )
