from typing import Literal

ObsRuleCoverageItemState = Literal["ambiguous", "changed", "matching", "missing", "unknown"]

OBS_RULE_COVERAGE_ITEM_STATE_VALUES: set[ObsRuleCoverageItemState] = {
    "ambiguous",
    "changed",
    "matching",
    "missing",
    "unknown",
}


def check_obs_rule_coverage_item_state(value: str) -> ObsRuleCoverageItemState:
    if value in OBS_RULE_COVERAGE_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_COVERAGE_ITEM_STATE_VALUES!r}")
