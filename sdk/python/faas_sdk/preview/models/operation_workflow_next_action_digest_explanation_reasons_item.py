from typing import Literal

OperationWorkflowNextActionDigestExplanationReasonsItem = Literal[
    "approaching_deadline", "overdue", "waiting_for_acceptance", "waiting_for_fulfillment_review"
]

OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_REASONS_ITEM_VALUES: set[
    OperationWorkflowNextActionDigestExplanationReasonsItem
] = {
    "approaching_deadline",
    "overdue",
    "waiting_for_acceptance",
    "waiting_for_fulfillment_review",
}


def check_operation_workflow_next_action_digest_explanation_reasons_item(
    value: str,
) -> OperationWorkflowNextActionDigestExplanationReasonsItem:
    if value in OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_REASONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_REASONS_ITEM_VALUES!r}"
    )
