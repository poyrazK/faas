from typing import Literal

AlertPresetResponseComparison = Literal["gt", "gte", "lt", "lte"]

ALERT_PRESET_RESPONSE_COMPARISON_VALUES: set[AlertPresetResponseComparison] = {
    "gt",
    "gte",
    "lt",
    "lte",
}


def check_alert_preset_response_comparison(value: str) -> AlertPresetResponseComparison:
    if value in ALERT_PRESET_RESPONSE_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_PRESET_RESPONSE_COMPARISON_VALUES!r}")
