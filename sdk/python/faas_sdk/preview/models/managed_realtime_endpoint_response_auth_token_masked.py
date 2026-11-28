from typing import Literal

ManagedRealtimeEndpointResponseAuthTokenMasked = Literal["***"]

MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_TOKEN_MASKED_VALUES: set[ManagedRealtimeEndpointResponseAuthTokenMasked] = {
    "***",
}


def check_managed_realtime_endpoint_response_auth_token_masked(
    value: str,
) -> ManagedRealtimeEndpointResponseAuthTokenMasked:
    if value in MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_TOKEN_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_TOKEN_MASKED_VALUES!r}"
    )
