from typing import Literal

OperationWorkflowObligationFulfillmentActionKind = Literal["coordinator_completion", "follow_up", "response_check_in"]

OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_ACTION_KIND_VALUES: set[OperationWorkflowObligationFulfillmentActionKind] = {
    "coordinator_completion",
    "follow_up",
    "response_check_in",
}


def check_operation_workflow_obligation_fulfillment_action_kind(
    value: str,
) -> OperationWorkflowObligationFulfillmentActionKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_ACTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_FULFILLMENT_ACTION_KIND_VALUES!r}"
    )
