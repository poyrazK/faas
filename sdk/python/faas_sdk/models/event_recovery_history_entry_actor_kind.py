from typing import Literal

EventRecoveryHistoryEntryActorKind = Literal["account", "api_key", "internal", "system"]

EVENT_RECOVERY_HISTORY_ENTRY_ACTOR_KIND_VALUES: set[EventRecoveryHistoryEntryActorKind] = {
    "account",
    "api_key",
    "internal",
    "system",
}


def check_event_recovery_history_entry_actor_kind(value: str) -> EventRecoveryHistoryEntryActorKind:
    if value in EVENT_RECOVERY_HISTORY_ENTRY_ACTOR_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_HISTORY_ENTRY_ACTOR_KIND_VALUES!r}")
