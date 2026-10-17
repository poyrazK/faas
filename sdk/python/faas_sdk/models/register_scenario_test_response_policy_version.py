from typing import Literal

RegisterScenarioTestResponsePolicyVersion = Literal[1]

REGISTER_SCENARIO_TEST_RESPONSE_POLICY_VERSION_VALUES: set[RegisterScenarioTestResponsePolicyVersion] = {
    1,
}


def check_register_scenario_test_response_policy_version(value: int) -> RegisterScenarioTestResponsePolicyVersion:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_POLICY_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_POLICY_VERSION_VALUES!r}"
    )
