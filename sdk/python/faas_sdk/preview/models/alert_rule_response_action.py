from typing import Literal

AlertRuleResponseAction = Literal["demote", "promote", "rollback", "webhook"]

ALERT_RULE_RESPONSE_ACTION_VALUES: set[AlertRuleResponseAction] = {
    "demote",
    "promote",
    "rollback",
    "webhook",
}


def check_alert_rule_response_action(value: str) -> AlertRuleResponseAction:
    if value in ALERT_RULE_RESPONSE_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_ACTION_VALUES!r}")
