from typing import Literal

AdminStatusEventUpdateRequestState = Literal[
    "cancelled", "completed", "identified", "in_progress", "investigating", "monitoring", "resolved", "scheduled"
]

ADMIN_STATUS_EVENT_UPDATE_REQUEST_STATE_VALUES: set[AdminStatusEventUpdateRequestState] = {
    "cancelled",
    "completed",
    "identified",
    "in_progress",
    "investigating",
    "monitoring",
    "resolved",
    "scheduled",
}


def check_admin_status_event_update_request_state(value: str) -> AdminStatusEventUpdateRequestState:
    if value in ADMIN_STATUS_EVENT_UPDATE_REQUEST_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ADMIN_STATUS_EVENT_UPDATE_REQUEST_STATE_VALUES!r}")
