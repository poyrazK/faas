from typing import Literal

CreateManagedRealtimeEndpointRequestAuthMode = Literal["none", "oidc_jwt", "static_bearer"]

CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES: set[CreateManagedRealtimeEndpointRequestAuthMode] = {
    "none",
    "oidc_jwt",
    "static_bearer",
}


def check_create_managed_realtime_endpoint_request_auth_mode(
    value: str,
) -> CreateManagedRealtimeEndpointRequestAuthMode:
    if value in CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_MODE_VALUES!r}"
    )
