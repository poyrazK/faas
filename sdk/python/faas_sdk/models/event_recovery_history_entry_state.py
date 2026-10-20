from typing import Literal

EventRecoveryHistoryEntryState = Literal["cancelled", "completed", "paused", "running"]

EVENT_RECOVERY_HISTORY_ENTRY_STATE_VALUES: set[EventRecoveryHistoryEntryState] = {
    "cancelled",
    "completed",
    "paused",
    "running",
}


def check_event_recovery_history_entry_state(value: str) -> EventRecoveryHistoryEntryState:
    if value in EVENT_RECOVERY_HISTORY_ENTRY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_HISTORY_ENTRY_STATE_VALUES!r}")
