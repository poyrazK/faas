from typing import Literal

WakeTimelineJSONRowTriggerClass = Literal["crawler", "monitor", "preview_bot", "unknown", "user"]

WAKE_TIMELINE_JSON_ROW_TRIGGER_CLASS_VALUES: set[WakeTimelineJSONRowTriggerClass] = {
    "crawler",
    "monitor",
    "preview_bot",
    "unknown",
    "user",
}


def check_wake_timeline_json_row_trigger_class(value: str) -> WakeTimelineJSONRowTriggerClass:
    if value in WAKE_TIMELINE_JSON_ROW_TRIGGER_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WAKE_TIMELINE_JSON_ROW_TRIGGER_CLASS_VALUES!r}")
