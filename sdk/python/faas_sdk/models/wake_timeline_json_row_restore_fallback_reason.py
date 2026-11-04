from typing import Literal

WakeTimelineJSONRowRestoreFallbackReason = Literal["after_restore_failed"]

WAKE_TIMELINE_JSON_ROW_RESTORE_FALLBACK_REASON_VALUES: set[WakeTimelineJSONRowRestoreFallbackReason] = {
    "after_restore_failed",
}


def check_wake_timeline_json_row_restore_fallback_reason(value: str) -> WakeTimelineJSONRowRestoreFallbackReason:
    if value in WAKE_TIMELINE_JSON_ROW_RESTORE_FALLBACK_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {WAKE_TIMELINE_JSON_ROW_RESTORE_FALLBACK_REASON_VALUES!r}"
    )
