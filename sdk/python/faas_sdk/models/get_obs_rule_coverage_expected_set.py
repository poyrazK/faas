from typing import Literal

GetObsRuleCoverageExpectedSet = Literal["gregale-core-v1", "gregale-full-v1"]

GET_OBS_RULE_COVERAGE_EXPECTED_SET_VALUES: set[GetObsRuleCoverageExpectedSet] = {
    "gregale-core-v1",
    "gregale-full-v1",
}


def check_get_obs_rule_coverage_expected_set(value: str) -> GetObsRuleCoverageExpectedSet:
    if value in GET_OBS_RULE_COVERAGE_EXPECTED_SET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_OBS_RULE_COVERAGE_EXPECTED_SET_VALUES!r}")
