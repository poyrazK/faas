from typing import Literal

EventRecoveryItemState = Literal["cancelled", "pending", "queued", "skipped"]

EVENT_RECOVERY_ITEM_STATE_VALUES: set[EventRecoveryItemState] = {
    "cancelled",
    "pending",
    "queued",
    "skipped",
}


def check_event_recovery_item_state(value: str) -> EventRecoveryItemState:
    if value in EVENT_RECOVERY_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_ITEM_STATE_VALUES!r}")
