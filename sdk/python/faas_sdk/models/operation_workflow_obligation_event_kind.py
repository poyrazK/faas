from typing import Literal

OperationWorkflowObligationEventKind = Literal[
    "accepted",
    "assigned",
    "canceled",
    "changed",
    "completed",
    "declined",
    "fulfilled",
    "fulfillment_reaffirmed",
    "fulfillment_review_coordinated",
    "fulfillment_review_escalated",
    "fulfillment_review_overdue",
    "fulfillment_review_required",
    "observed_assignment",
    "reassigned",
    "reopened",
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
    "fulfillment_reaffirmed",
    "fulfillment_review_coordinated",
    "fulfillment_review_escalated",
    "fulfillment_review_overdue",
    "fulfillment_review_required",
    "observed_assignment",
    "reassigned",
    "reopened",
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
