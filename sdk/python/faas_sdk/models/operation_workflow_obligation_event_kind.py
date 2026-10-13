from typing import Literal

OperationWorkflowObligationEventKind = Literal[
    "accepted", "canceled", "completed", "declined", "observed_assignment", "reassigned", "rescheduled"
]

OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES: set[OperationWorkflowObligationEventKind] = {
    "accepted",
    "canceled",
    "completed",
    "declined",
    "observed_assignment",
    "reassigned",
    "rescheduled",
}


def check_operation_workflow_obligation_event_kind(value: str) -> OperationWorkflowObligationEventKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES!r}")
