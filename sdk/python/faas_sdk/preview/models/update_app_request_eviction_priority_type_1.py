from typing import Literal

UpdateAppRequestEvictionPriorityType1 = Literal["best_effort", "reserved"]

UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_1_VALUES: set[UpdateAppRequestEvictionPriorityType1] = {
    "best_effort",
    "reserved",
}


def check_update_app_request_eviction_priority_type_1(value: str) -> UpdateAppRequestEvictionPriorityType1:
    if value in UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_1_VALUES!r}"
    )
