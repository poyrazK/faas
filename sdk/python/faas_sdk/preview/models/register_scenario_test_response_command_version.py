from typing import Literal

RegisterScenarioTestResponseCommandVersion = Literal[1]

REGISTER_SCENARIO_TEST_RESPONSE_COMMAND_VERSION_VALUES: set[RegisterScenarioTestResponseCommandVersion] = {
    1,
}


def check_register_scenario_test_response_command_version(value: int) -> RegisterScenarioTestResponseCommandVersion:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_COMMAND_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_COMMAND_VERSION_VALUES!r}"
    )
