from typing import Literal

ObsRuleCoverageSelectionSource = Literal["explicit_request", "none"]

OBS_RULE_COVERAGE_SELECTION_SOURCE_VALUES: set[ObsRuleCoverageSelectionSource] = {
    "explicit_request",
    "none",
}


def check_obs_rule_coverage_selection_source(value: str) -> ObsRuleCoverageSelectionSource:
    if value in OBS_RULE_COVERAGE_SELECTION_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_SELECTION_SOURCE_VALUES!r}")
