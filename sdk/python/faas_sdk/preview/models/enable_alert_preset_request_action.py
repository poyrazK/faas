from typing import Literal

EnableAlertPresetRequestAction = Literal["demote", "promote", "rollback", "webhook"]

ENABLE_ALERT_PRESET_REQUEST_ACTION_VALUES: set[EnableAlertPresetRequestAction] = {
    "demote",
    "promote",
    "rollback",
    "webhook",
}


def check_enable_alert_preset_request_action(value: str) -> EnableAlertPresetRequestAction:
    if value in ENABLE_ALERT_PRESET_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENABLE_ALERT_PRESET_REQUEST_ACTION_VALUES!r}")
