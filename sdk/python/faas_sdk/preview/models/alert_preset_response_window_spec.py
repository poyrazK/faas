from typing import Literal

AlertPresetResponseWindowSpec = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

ALERT_PRESET_RESPONSE_WINDOW_SPEC_VALUES: set[AlertPresetResponseWindowSpec] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_alert_preset_response_window_spec(value: str) -> AlertPresetResponseWindowSpec:
    if value in ALERT_PRESET_RESPONSE_WINDOW_SPEC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_PRESET_RESPONSE_WINDOW_SPEC_VALUES!r}")
