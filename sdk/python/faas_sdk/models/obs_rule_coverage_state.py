from typing import Literal

ObsRuleCoverageState = Literal["covered", "degraded", "partial", "unknown"]

OBS_RULE_COVERAGE_STATE_VALUES: set[ObsRuleCoverageState] = {
    "covered",
    "degraded",
    "partial",
    "unknown",
}


def check_obs_rule_coverage_state(value: str) -> ObsRuleCoverageState:
    if value in OBS_RULE_COVERAGE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_STATE_VALUES!r}")
