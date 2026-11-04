from typing import Literal

OperationWorkflowObligationReopeningActionKind = Literal["coordinator_completion", "follow_up", "response_check_in"]

OPERATION_WORKFLOW_OBLIGATION_REOPENING_ACTION_KIND_VALUES: set[OperationWorkflowObligationReopeningActionKind] = {
    "coordinator_completion",
    "follow_up",
    "response_check_in",
}


def check_operation_workflow_obligation_reopening_action_kind(
    value: str,
) -> OperationWorkflowObligationReopeningActionKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_REOPENING_ACTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_REOPENING_ACTION_KIND_VALUES!r}"
    )
