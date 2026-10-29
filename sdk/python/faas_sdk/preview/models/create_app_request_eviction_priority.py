from typing import Literal

CreateAppRequestEvictionPriority = Literal["best_effort", "reserved"]

CREATE_APP_REQUEST_EVICTION_PRIORITY_VALUES: set[CreateAppRequestEvictionPriority] = {
    "best_effort",
    "reserved",
}


def check_create_app_request_eviction_priority(value: str) -> CreateAppRequestEvictionPriority:
    if value in CREATE_APP_REQUEST_EVICTION_PRIORITY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_EVICTION_PRIORITY_VALUES!r}")
