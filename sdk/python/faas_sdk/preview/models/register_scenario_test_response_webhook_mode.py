from typing import Literal

RegisterScenarioTestResponseWebhookMode = Literal["capture"]

REGISTER_SCENARIO_TEST_RESPONSE_WEBHOOK_MODE_VALUES: set[RegisterScenarioTestResponseWebhookMode] = {
    "capture",
}


def check_register_scenario_test_response_webhook_mode(value: str) -> RegisterScenarioTestResponseWebhookMode:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_WEBHOOK_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_WEBHOOK_MODE_VALUES!r}"
    )
