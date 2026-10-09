from typing import Literal

EventRecoveryHistoryEntryAction = Literal["cancelled", "created", "expired", "paused", "rate_changed", "resumed"]

EVENT_RECOVERY_HISTORY_ENTRY_ACTION_VALUES: set[EventRecoveryHistoryEntryAction] = {
    "cancelled",
    "created",
    "expired",
    "paused",
    "rate_changed",
    "resumed",
}


def check_event_recovery_history_entry_action(value: str) -> EventRecoveryHistoryEntryAction:
    if value in EVENT_RECOVERY_HISTORY_ENTRY_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_HISTORY_ENTRY_ACTION_VALUES!r}")
