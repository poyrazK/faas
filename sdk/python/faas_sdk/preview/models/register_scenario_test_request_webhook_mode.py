from typing import Literal

RegisterScenarioTestRequestWebhookMode = Literal["capture"]

REGISTER_SCENARIO_TEST_REQUEST_WEBHOOK_MODE_VALUES: set[RegisterScenarioTestRequestWebhookMode] = {
    "capture",
}


def check_register_scenario_test_request_webhook_mode(value: str) -> RegisterScenarioTestRequestWebhookMode:
    if value in REGISTER_SCENARIO_TEST_REQUEST_WEBHOOK_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_REQUEST_WEBHOOK_MODE_VALUES!r}"
    )
