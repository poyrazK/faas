from typing import Literal

ObsRuleHealthResponseState = Literal["degraded", "disabled", "healthy", "unavailable"]

OBS_RULE_HEALTH_RESPONSE_STATE_VALUES: set[ObsRuleHealthResponseState] = {
    "degraded",
    "disabled",
    "healthy",
    "unavailable",
}


def check_obs_rule_health_response_state(value: str) -> ObsRuleHealthResponseState:
    if value in OBS_RULE_HEALTH_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RULE_HEALTH_RESPONSE_STATE_VALUES!r}")
