from typing import Literal

AlertPresetResponseMinimumPlan = Literal["free", "hobby", "pro", "scale"]

ALERT_PRESET_RESPONSE_MINIMUM_PLAN_VALUES: set[AlertPresetResponseMinimumPlan] = {
    "free",
    "hobby",
    "pro",
    "scale",
}


def check_alert_preset_response_minimum_plan(value: str) -> AlertPresetResponseMinimumPlan:
    if value in ALERT_PRESET_RESPONSE_MINIMUM_PLAN_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_PRESET_RESPONSE_MINIMUM_PLAN_VALUES!r}")
