from typing import Literal

RegisterScenarioTestRequestNetworkMode = Literal["contained"]

REGISTER_SCENARIO_TEST_REQUEST_NETWORK_MODE_VALUES: set[RegisterScenarioTestRequestNetworkMode] = {
    "contained",
}


def check_register_scenario_test_request_network_mode(value: str) -> RegisterScenarioTestRequestNetworkMode:
    if value in REGISTER_SCENARIO_TEST_REQUEST_NETWORK_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_REQUEST_NETWORK_MODE_VALUES!r}"
    )
