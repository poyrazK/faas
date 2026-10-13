from typing import Literal

OperationWorkflowObligationEventKind = Literal[
    "accepted",
    "assigned",
    "canceled",
    "changed",
    "completed",
    "declined",
    "fulfilled",
    "observed_assignment",
    "reassigned",
    "replaced_by",
    "replaces",
    "rescheduled",
    "superseded",
    "target_bound",
    "target_cleared",
]

OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES: set[OperationWorkflowObligationEventKind] = {
    "accepted",
    "assigned",
    "canceled",
    "changed",
    "completed",
    "declined",
    "fulfilled",
    "observed_assignment",
    "reassigned",
    "replaced_by",
    "replaces",
    "rescheduled",
    "superseded",
    "target_bound",
    "target_cleared",
}


def check_operation_workflow_obligation_event_kind(value: str) -> OperationWorkflowObligationEventKind:
    if value in OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_WORKFLOW_OBLIGATION_EVENT_KIND_VALUES!r}")
