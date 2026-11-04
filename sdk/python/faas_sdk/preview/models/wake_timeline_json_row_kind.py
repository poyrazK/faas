from typing import Literal

WakeTimelineJSONRowKind = Literal["wake.boot_started"]

WAKE_TIMELINE_JSON_ROW_KIND_VALUES: set[WakeTimelineJSONRowKind] = {
    "wake.boot_started",
}


def check_wake_timeline_json_row_kind(value: str) -> WakeTimelineJSONRowKind:
    if value in WAKE_TIMELINE_JSON_ROW_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WAKE_TIMELINE_JSON_ROW_KIND_VALUES!r}")
