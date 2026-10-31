from typing import Literal

PublicStatusEventState = Literal[
    "cancelled", "completed", "identified", "in_progress", "investigating", "monitoring", "resolved", "scheduled"
]

PUBLIC_STATUS_EVENT_STATE_VALUES: set[PublicStatusEventState] = {
    "cancelled",
    "completed",
    "identified",
    "in_progress",
    "investigating",
    "monitoring",
    "resolved",
    "scheduled",
}


def check_public_status_event_state(value: str) -> PublicStatusEventState:
    if value in PUBLIC_STATUS_EVENT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_EVENT_STATE_VALUES!r}")
