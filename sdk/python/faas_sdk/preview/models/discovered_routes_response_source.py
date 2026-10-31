from typing import Literal

DiscoveredRoutesResponseSource = Literal["usage_outbox"]

DISCOVERED_ROUTES_RESPONSE_SOURCE_VALUES: set[DiscoveredRoutesResponseSource] = {
    "usage_outbox",
}


def check_discovered_routes_response_source(value: str) -> DiscoveredRoutesResponseSource:
    if value in DISCOVERED_ROUTES_RESPONSE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DISCOVERED_ROUTES_RESPONSE_SOURCE_VALUES!r}")
