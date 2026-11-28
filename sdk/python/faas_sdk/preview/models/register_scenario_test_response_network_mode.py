from typing import Literal

RegisterScenarioTestResponseNetworkMode = Literal["contained"]

REGISTER_SCENARIO_TEST_RESPONSE_NETWORK_MODE_VALUES: set[RegisterScenarioTestResponseNetworkMode] = {
    "contained",
}


def check_register_scenario_test_response_network_mode(value: str) -> RegisterScenarioTestResponseNetworkMode:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_NETWORK_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_NETWORK_MODE_VALUES!r}"
    )
