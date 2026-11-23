from typing import Literal

RotateManagedRealtimeAuthResponseAuthMode = Literal["static_bearer"]

ROTATE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES: set[RotateManagedRealtimeAuthResponseAuthMode] = {
    "static_bearer",
}


def check_rotate_managed_realtime_auth_response_auth_mode(value: str) -> RotateManagedRealtimeAuthResponseAuthMode:
    if value in ROTATE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ROTATE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES!r}"
    )
