from typing import Literal

PublicStatusUpdateState = Literal[
    "cancelled", "completed", "identified", "in_progress", "investigating", "monitoring", "resolved", "scheduled"
]

PUBLIC_STATUS_UPDATE_STATE_VALUES: set[PublicStatusUpdateState] = {
    "cancelled",
    "completed",
    "identified",
    "in_progress",
    "investigating",
    "monitoring",
    "resolved",
    "scheduled",
}


def check_public_status_update_state(value: str) -> PublicStatusUpdateState:
    if value in PUBLIC_STATUS_UPDATE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_UPDATE_STATE_VALUES!r}")
