from typing import Literal

FinalizeManagedRealtimeAuthResponseAuthMode = Literal["static_bearer"]

FINALIZE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES: set[FinalizeManagedRealtimeAuthResponseAuthMode] = {
    "static_bearer",
}


def check_finalize_managed_realtime_auth_response_auth_mode(value: str) -> FinalizeManagedRealtimeAuthResponseAuthMode:
    if value in FINALIZE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {FINALIZE_MANAGED_REALTIME_AUTH_RESPONSE_AUTH_MODE_VALUES!r}"
    )
