from typing import Literal

EventRecoveryPreflightItemStatus = Literal["eligible", "likely_skipped", "unknown", "waiting"]

EVENT_RECOVERY_PREFLIGHT_ITEM_STATUS_VALUES: set[EventRecoveryPreflightItemStatus] = {
    "eligible",
    "likely_skipped",
    "unknown",
    "waiting",
}


def check_event_recovery_preflight_item_status(value: str) -> EventRecoveryPreflightItemStatus:
    if value in EVENT_RECOVERY_PREFLIGHT_ITEM_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_ITEM_STATUS_VALUES!r}")
