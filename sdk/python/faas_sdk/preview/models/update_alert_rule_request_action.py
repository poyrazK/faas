from typing import Literal

UpdateAlertRuleRequestAction = Literal["demote", "promote", "rollback", "webhook"]

UPDATE_ALERT_RULE_REQUEST_ACTION_VALUES: set[UpdateAlertRuleRequestAction] = {
    "demote",
    "promote",
    "rollback",
    "webhook",
}


def check_update_alert_rule_request_action(value: str) -> UpdateAlertRuleRequestAction:
    if value in UPDATE_ALERT_RULE_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_ALERT_RULE_REQUEST_ACTION_VALUES!r}")
