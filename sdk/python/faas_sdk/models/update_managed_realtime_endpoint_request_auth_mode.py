from typing import Literal

UpdateManagedRealtimeEndpointRequestAuthMode = Literal["none", "oidc_jwt", "static_bearer"]

UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES: set[UpdateManagedRealtimeEndpointRequestAuthMode] = {
    "none",
    "oidc_jwt",
    "static_bearer",
}


def check_update_managed_realtime_endpoint_request_auth_mode(
    value: str,
) -> UpdateManagedRealtimeEndpointRequestAuthMode:
    if value in UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES!r}"
    )
