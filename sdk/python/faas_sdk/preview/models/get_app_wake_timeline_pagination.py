from typing import Literal

GetAppWakeTimelinePagination = Literal["keyset_v1"]

GET_APP_WAKE_TIMELINE_PAGINATION_VALUES: set[GetAppWakeTimelinePagination] = {
    "keyset_v1",
}


def check_get_app_wake_timeline_pagination(value: str) -> GetAppWakeTimelinePagination:
    if value in GET_APP_WAKE_TIMELINE_PAGINATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_APP_WAKE_TIMELINE_PAGINATION_VALUES!r}")
