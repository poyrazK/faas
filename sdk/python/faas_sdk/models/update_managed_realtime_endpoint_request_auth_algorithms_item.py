from typing import Literal

UpdateManagedRealtimeEndpointRequestAuthAlgorithmsItem = Literal["ES256", "ES384", "ES512", "RS256", "RS384", "RS512"]

UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES: set[
    UpdateManagedRealtimeEndpointRequestAuthAlgorithmsItem
] = {
    "ES256",
    "ES384",
    "ES512",
    "RS256",
    "RS384",
    "RS512",
}


def check_update_managed_realtime_endpoint_request_auth_algorithms_item(
    value: str,
) -> UpdateManagedRealtimeEndpointRequestAuthAlgorithmsItem:
    if value in UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES!r}"
    )
