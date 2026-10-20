from typing import Literal

EventRecoveryPreflightItemSavedOutcome = Literal[
    "cancelled",
    "dead_lettered",
    "expired",
    "failed",
    "queued",
    "retrying",
    "running",
    "succeeded",
    "superseded",
    "unknown",
]

EVENT_RECOVERY_PREFLIGHT_ITEM_SAVED_OUTCOME_VALUES: set[EventRecoveryPreflightItemSavedOutcome] = {
    "cancelled",
    "dead_lettered",
    "expired",
    "failed",
    "queued",
    "retrying",
    "running",
    "succeeded",
    "superseded",
    "unknown",
}


def check_event_recovery_preflight_item_saved_outcome(value: str) -> EventRecoveryPreflightItemSavedOutcome:
    if value in EVENT_RECOVERY_PREFLIGHT_ITEM_SAVED_OUTCOME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_ITEM_SAVED_OUTCOME_VALUES!r}"
    )
