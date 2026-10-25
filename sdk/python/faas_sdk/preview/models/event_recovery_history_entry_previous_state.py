from typing import Literal

EventRecoveryHistoryEntryPreviousState = Literal["", "cancelled", "completed", "paused", "running"]

EVENT_RECOVERY_HISTORY_ENTRY_PREVIOUS_STATE_VALUES: set[EventRecoveryHistoryEntryPreviousState] = {
    "",
    "cancelled",
    "completed",
    "paused",
    "running",
}


def check_event_recovery_history_entry_previous_state(value: str) -> EventRecoveryHistoryEntryPreviousState:
    if value in EVENT_RECOVERY_HISTORY_ENTRY_PREVIOUS_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_HISTORY_ENTRY_PREVIOUS_STATE_VALUES!r}"
    )
