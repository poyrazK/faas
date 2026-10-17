from typing import Literal

WakeTimelinePageMode = Literal["keyset_v1"]

WAKE_TIMELINE_PAGE_MODE_VALUES: set[WakeTimelinePageMode] = {
    "keyset_v1",
}


def check_wake_timeline_page_mode(value: str) -> WakeTimelinePageMode:
    if value in WAKE_TIMELINE_PAGE_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WAKE_TIMELINE_PAGE_MODE_VALUES!r}")
