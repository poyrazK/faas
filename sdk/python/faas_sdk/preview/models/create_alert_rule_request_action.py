from typing import Literal

CreateAlertRuleRequestAction = Literal["demote", "promote", "rollback", "webhook"]

CREATE_ALERT_RULE_REQUEST_ACTION_VALUES: set[CreateAlertRuleRequestAction] = {
    "demote",
    "promote",
    "rollback",
    "webhook",
}


def check_create_alert_rule_request_action(value: str) -> CreateAlertRuleRequestAction:
    if value in CREATE_ALERT_RULE_REQUEST_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ALERT_RULE_REQUEST_ACTION_VALUES!r}")
