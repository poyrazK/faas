from typing import Literal

ObsRuleHealthItemState = Literal["failed", "healthy", "invalid", "invalid_clock", "stale", "unavailable"]

OBS_RULE_HEALTH_ITEM_STATE_VALUES: set[ObsRuleHealthItemState] = {
    "failed",
    "healthy",
    "invalid",
    "invalid_clock",
    "stale",
    "unavailable",
}


def check_obs_rule_health_item_state(value: str) -> ObsRuleHealthItemState:
    if value in OBS_RULE_HEALTH_ITEM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_HEALTH_ITEM_STATE_VALUES!r}")
