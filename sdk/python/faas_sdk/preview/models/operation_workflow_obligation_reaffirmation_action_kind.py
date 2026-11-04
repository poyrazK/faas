from typing import Literal

OperationWorkflowObligationReaffirmationActionKind = Literal["coordinator_completion", "follow_up", "response_check_in"]

OPERATION_WORKFLOW_OBLIGATION_REAFFIRMATION_ACTION_KIND_VALUES: set[
    OperationWorkflowObligationReaffirmationActionKind
] = {
    "coordinator_completion",
    "follow_up",
    "response_check_in",
}


def check_operation_workflow_obligation_reaffirmation_action_kind(
    value: str,
) -> OperationWorkflowObligationReaffirmationActionKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_REAFFIRMATION_ACTION_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_REAFFIRMATION_ACTION_KIND_VALUES!r}"
    )
