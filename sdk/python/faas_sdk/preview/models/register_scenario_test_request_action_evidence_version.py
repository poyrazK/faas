from typing import Literal

RegisterScenarioTestRequestActionEvidenceVersion = Literal[1, 2]

REGISTER_SCENARIO_TEST_REQUEST_ACTION_EVIDENCE_VERSION_VALUES: set[RegisterScenarioTestRequestActionEvidenceVersion] = {
    1,
    2,
}


def check_register_scenario_test_request_action_evidence_version(
    value: int,
) -> RegisterScenarioTestRequestActionEvidenceVersion:
    if value in REGISTER_SCENARIO_TEST_REQUEST_ACTION_EVIDENCE_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_REQUEST_ACTION_EVIDENCE_VERSION_VALUES!r}"
    )
