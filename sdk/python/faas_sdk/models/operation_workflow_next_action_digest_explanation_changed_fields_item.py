from typing import Literal

OperationWorkflowNextActionDigestExplanationChangedFieldsItem = Literal[
    "cases",
    "deadline",
    "evidence",
    "fulfillment_review",
    "obligation",
    "overdue",
    "owner",
    "scope",
    "shared_plan",
    "subscription_tenant_id",
    "summary",
    "trigger",
]

OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_CHANGED_FIELDS_ITEM_VALUES: set[
    OperationWorkflowNextActionDigestExplanationChangedFieldsItem
] = {
    "cases",
    "deadline",
    "evidence",
    "fulfillment_review",
    "obligation",
    "overdue",
    "owner",
    "scope",
    "shared_plan",
    "subscription_tenant_id",
    "summary",
    "trigger",
}


def check_operation_workflow_next_action_digest_explanation_changed_fields_item(
    value: str,
) -> OperationWorkflowNextActionDigestExplanationChangedFieldsItem:
    if value in OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_CHANGED_FIELDS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_EXPLANATION_CHANGED_FIELDS_ITEM_VALUES!r}"
    )
