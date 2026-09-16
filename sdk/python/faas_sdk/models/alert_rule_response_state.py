from typing import Literal

AlertRuleResponseState = Literal["degraded", "firing", "ok"]

ALERT_RULE_RESPONSE_STATE_VALUES: set[AlertRuleResponseState] = {
    "degraded",
    "firing",
    "ok",
}


def check_alert_rule_response_state(value: str) -> AlertRuleResponseState:
    if value in ALERT_RULE_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_STATE_VALUES!r}")
