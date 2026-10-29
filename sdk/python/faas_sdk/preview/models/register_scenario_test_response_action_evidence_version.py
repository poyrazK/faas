from typing import Literal

RegisterScenarioTestResponseActionEvidenceVersion = Literal[1, 2]

REGISTER_SCENARIO_TEST_RESPONSE_ACTION_EVIDENCE_VERSION_VALUES: set[
    RegisterScenarioTestResponseActionEvidenceVersion
] = {
    1,
    2,
}


def check_register_scenario_test_response_action_evidence_version(
    value: int,
) -> RegisterScenarioTestResponseActionEvidenceVersion:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_ACTION_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_ACTION_EVIDENCE_VERSION_VALUES!r}"
    )
