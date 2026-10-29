from typing import Literal

RegisterScenarioTestRequestCommandVersion = Literal[1]

REGISTER_SCENARIO_TEST_REQUEST_COMMAND_VERSION_VALUES: set[RegisterScenarioTestRequestCommandVersion] = {
    1,
}


def check_register_scenario_test_request_command_version(value: int) -> RegisterScenarioTestRequestCommandVersion:
    if value in REGISTER_SCENARIO_TEST_REQUEST_COMMAND_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_REQUEST_COMMAND_VERSION_VALUES!r}"
    )
