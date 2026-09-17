from typing import Literal

ManagedRealtimeEndpointResponseAuthAlgorithmsItem = Literal["ES256", "ES384", "ES512", "RS256", "RS384", "RS512"]

MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_ALGORITHMS_ITEM_VALUES: set[
    ManagedRealtimeEndpointResponseAuthAlgorithmsItem
] = {
    "ES256",
    "ES384",
    "ES512",
    "RS256",
    "RS384",
    "RS512",
}


def check_managed_realtime_endpoint_response_auth_algorithms_item(
    value: str,
) -> ManagedRealtimeEndpointResponseAuthAlgorithmsItem:
    if value in MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_ALGORITHMS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_ENDPOINT_RESPONSE_AUTH_ALGORITHMS_ITEM_VALUES!r}"
    )
