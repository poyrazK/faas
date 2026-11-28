from typing import Literal

RegisterScenarioTestResponseRecorderVersion = Literal[1]

REGISTER_SCENARIO_TEST_RESPONSE_RECORDER_VERSION_VALUES: set[RegisterScenarioTestResponseRecorderVersion] = {
    1,
}


def check_register_scenario_test_response_recorder_version(value: int) -> RegisterScenarioTestResponseRecorderVersion:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_RECORDER_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_RECORDER_VERSION_VALUES!r}"
    )
