from typing import Literal

CreateManagedRealtimeEndpointRequestAuthAlgorithmsItem = Literal["ES256", "ES384", "ES512", "RS256", "RS384", "RS512"]

CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES: set[
    CreateManagedRealtimeEndpointRequestAuthAlgorithmsItem
] = {
    "ES256",
    "ES384",
    "ES512",
    "RS256",
    "RS384",
    "RS512",
}


def check_create_managed_realtime_endpoint_request_auth_algorithms_item(
    value: str,
) -> CreateManagedRealtimeEndpointRequestAuthAlgorithmsItem:
    if value in CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_MANAGED_REALTIME_ENDPOINT_REQUEST_AUTH_ALGORITHMS_ITEM_VALUES!r}"
    )
