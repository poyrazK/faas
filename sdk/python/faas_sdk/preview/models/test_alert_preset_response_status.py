from typing import Literal

TestAlertPresetResponseStatus = Literal["sent"]

TEST_ALERT_PRESET_RESPONSE_STATUS_VALUES: set[TestAlertPresetResponseStatus] = {
    "sent",
}


def check_test_alert_preset_response_status(value: str) -> TestAlertPresetResponseStatus:
    if value in TEST_ALERT_PRESET_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TEST_ALERT_PRESET_RESPONSE_STATUS_VALUES!r}")
