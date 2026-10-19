from typing import Literal

ObsRuleHealthItemReportedState = Literal["firing", "inactive", "pending", "unknown"]

OBS_RULE_HEALTH_ITEM_REPORTED_STATE_VALUES: set[ObsRuleHealthItemReportedState] = {
    "firing",
    "inactive",
    "pending",
    "unknown",
}


def check_obs_rule_health_item_reported_state(value: str) -> ObsRuleHealthItemReportedState:
    if value in OBS_RULE_HEALTH_ITEM_REPORTED_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_HEALTH_ITEM_REPORTED_STATE_VALUES!r}")
