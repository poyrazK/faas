from typing import Literal

UpdateAppRequestEvictionPriorityType3Type1 = Literal["best_effort", "reserved"]

UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_3_TYPE_1_VALUES: set[UpdateAppRequestEvictionPriorityType3Type1] = {
    "best_effort",
    "reserved",
}


def check_update_app_request_eviction_priority_type_3_type_1(value: str) -> UpdateAppRequestEvictionPriorityType3Type1:
    if value in UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_APP_REQUEST_EVICTION_PRIORITY_TYPE_3_TYPE_1_VALUES!r}"
    )
