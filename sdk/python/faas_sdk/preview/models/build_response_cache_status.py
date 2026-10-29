from typing import Literal

BuildResponseCacheStatus = Literal["hit", "invalidated", "miss"]

BUILD_RESPONSE_CACHE_STATUS_VALUES: set[BuildResponseCacheStatus] = {
    "hit",
    "invalidated",
    "miss",
}


def check_build_response_cache_status(value: str) -> BuildResponseCacheStatus:
    if value in BUILD_RESPONSE_CACHE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {BUILD_RESPONSE_CACHE_STATUS_VALUES!r}")
