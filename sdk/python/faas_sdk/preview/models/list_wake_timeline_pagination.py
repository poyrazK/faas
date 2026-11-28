from typing import Literal

ListWakeTimelinePagination = Literal["keyset_v1"]

LIST_WAKE_TIMELINE_PAGINATION_VALUES: set[ListWakeTimelinePagination] = {
    "keyset_v1",
}


def check_list_wake_timeline_pagination(value: str) -> ListWakeTimelinePagination:
    if value in LIST_WAKE_TIMELINE_PAGINATION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_WAKE_TIMELINE_PAGINATION_VALUES!r}")
