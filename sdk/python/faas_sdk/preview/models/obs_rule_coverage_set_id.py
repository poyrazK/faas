from typing import Literal

ObsRuleCoverageSetId = Literal["gregale-core-v1", "gregale-full-v1"]

OBS_RULE_COVERAGE_SET_ID_VALUES: set[ObsRuleCoverageSetId] = {
    "gregale-core-v1",
    "gregale-full-v1",
}


def check_obs_rule_coverage_set_id(value: str) -> ObsRuleCoverageSetId:
    if value in OBS_RULE_COVERAGE_SET_ID_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_SET_ID_VALUES!r}")
