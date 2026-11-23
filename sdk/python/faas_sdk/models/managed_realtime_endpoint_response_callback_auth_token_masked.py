from typing import Literal

ManagedRealtimeEndpointResponseCallbackAuthTokenMasked = Literal["***"]

MANAGED_REALTIME_ENDPOINT_RESPONSE_CALLBACK_AUTH_TOKEN_MASKED_VALUES: set[
    ManagedRealtimeEndpointResponseCallbackAuthTokenMasked
] = {
    "***",
}


def check_managed_realtime_endpoint_response_callback_auth_token_masked(
    value: str,
) -> ManagedRealtimeEndpointResponseCallbackAuthTokenMasked:
    if value in MANAGED_REALTIME_ENDPOINT_RESPONSE_CALLBACK_AUTH_TOKEN_MASKED_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_ENDPOINT_RESPONSE_CALLBACK_AUTH_TOKEN_MASKED_VALUES!r}"
    )
