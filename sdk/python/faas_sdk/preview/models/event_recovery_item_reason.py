from typing import Literal

EventRecoveryItemReason = Literal["cancelled", "changed", "expired", "receipt_expired", "target_unavailable"]

EVENT_RECOVERY_ITEM_REASON_VALUES: set[EventRecoveryItemReason] = {
    "cancelled",
    "changed",
    "expired",
    "receipt_expired",
    "target_unavailable",
}


def check_event_recovery_item_reason(value: str) -> EventRecoveryItemReason:
    if value in EVENT_RECOVERY_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_ITEM_REASON_VALUES!r}")
