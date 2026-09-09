from typing import Literal

BuildResponseCacheStatus = Literal["hit", "miss", "invalidated"]

BUILD_RESPONSE_CACHE_STATUS_VALUES: set[BuildResponseCacheStatus] = {
    "hit",
    "miss",
    "invalidated",
}


def check_build_response_cache_status(value: str) -> BuildResponseCacheStatus:
    if value in BUILD_RESPONSE_CACHE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_RESPONSE_CACHE_STATUS_VALUES!r}")
