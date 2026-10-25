from typing import Literal

AppResponseEvictionPriority = Literal["best_effort", "reserved"]

APP_RESPONSE_EVICTION_PRIORITY_VALUES: set[AppResponseEvictionPriority] = {
    "best_effort",
    "reserved",
}


def check_app_response_eviction_priority(value: str) -> AppResponseEvictionPriority:
    if value in APP_RESPONSE_EVICTION_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_RESPONSE_EVICTION_PRIORITY_VALUES!r}")
