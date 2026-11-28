from typing import Literal

OperationWorkflowNextActionDigestCheckItemStatus = Literal[
    "canceled", "changed", "completed", "no_longer_outstanding", "outstanding", "replaced", "unavailable"
]

OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_CHECK_ITEM_STATUS_VALUES: set[
    OperationWorkflowNextActionDigestCheckItemStatus
] = {
    "canceled",
    "changed",
    "completed",
    "no_longer_outstanding",
    "outstanding",
    "replaced",
    "unavailable",
}


def check_operation_workflow_next_action_digest_check_item_status(
    value: str,
) -> OperationWorkflowNextActionDigestCheckItemStatus:
    if value in OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_CHECK_ITEM_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_NEXT_ACTION_DIGEST_CHECK_ITEM_STATUS_VALUES!r}"
    )
