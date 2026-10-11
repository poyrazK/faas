from typing import Literal

OperationEventType = Literal[
    "accepted",
    "artifact_attached",
    "cancellation_requested",
    "cancelled",
    "delivery_changed",
    "failed",
    "progress",
    "reconciliation_required",
    "recovery_requested",
    "running",
    "succeeded",
]

OPERATION_EVENT_TYPE_VALUES: set[OperationEventType] = {
    "accepted",
    "artifact_attached",
    "cancellation_requested",
    "cancelled",
    "delivery_changed",
    "failed",
    "progress",
    "reconciliation_required",
    "recovery_requested",
    "running",
    "succeeded",
}


def check_operation_event_type(value: str) -> OperationEventType:
    if value in OPERATION_EVENT_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OPERATION_EVENT_TYPE_VALUES!r}")
