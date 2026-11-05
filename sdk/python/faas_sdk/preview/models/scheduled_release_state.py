from typing import Literal

ScheduledReleaseState = Literal["blocked", "cancelled", "expired", "scheduled", "started", "superseded"]

SCHEDULED_RELEASE_STATE_VALUES: set[ScheduledReleaseState] = {
    "blocked",
    "cancelled",
    "expired",
    "scheduled",
    "started",
    "superseded",
}


def check_scheduled_release_state(value: str) -> ScheduledReleaseState:
    if value in SCHEDULED_RELEASE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCHEDULED_RELEASE_STATE_VALUES!r}")
