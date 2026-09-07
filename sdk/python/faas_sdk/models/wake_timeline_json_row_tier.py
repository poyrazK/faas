from typing import Literal

WakeTimelineJSONRowTier = Literal["cold_boot_fallback", "init", "warm"]

WAKE_TIMELINE_JSON_ROW_TIER_VALUES: set[WakeTimelineJSONRowTier] = {
    "cold_boot_fallback",
    "init",
    "warm",
}


def check_wake_timeline_json_row_tier(value: str) -> WakeTimelineJSONRowTier:
    if value in WAKE_TIMELINE_JSON_ROW_TIER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {WAKE_TIMELINE_JSON_ROW_TIER_VALUES!r}")
