from typing import Literal

ManagedRealtimeAccessRequestAction = Literal["restore_channel", "revoke_channel", "revoke_sessions"]

MANAGED_REALTIME_ACCESS_REQUEST_ACTION_VALUES: set[ManagedRealtimeAccessRequestAction] = {
    "restore_channel",
    "revoke_channel",
    "revoke_sessions",
}


def check_managed_realtime_access_request_action(value: str) -> ManagedRealtimeAccessRequestAction:
    if value in MANAGED_REALTIME_ACCESS_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_ACCESS_REQUEST_ACTION_VALUES!r}")
