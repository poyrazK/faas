from typing import Literal

ManagedRealtimeEndpointResponseAuthMode = Literal["none", "oidc_jwt", "static_bearer"]

MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_MODE_VALUES: set[ManagedRealtimeEndpointResponseAuthMode] = {
    "none",
    "oidc_jwt",
    "static_bearer",
}


def check_managed_realtime_endpoint_response_auth_mode(value: str) -> ManagedRealtimeEndpointResponseAuthMode:
    if value in MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_MODE_VALUES!r}"
    )
