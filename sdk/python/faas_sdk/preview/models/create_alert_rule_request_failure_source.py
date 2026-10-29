from typing import Literal

CreateAlertRuleRequestFailureSource = Literal["any", "async_invoke", "cron", "delayed_task", "inbound_webhook", "queue"]

CREATE_ALERT_RULE_REQUEST_FAILURE_SOURCE_VALUES: set[CreateAlertRuleRequestFailureSource] = {
    "any",
    "async_invoke",
    "cron",
    "delayed_task",
    "inbound_webhook",
    "queue",
}


def check_create_alert_rule_request_failure_source(value: str) -> CreateAlertRuleRequestFailureSource:
    if value in CREATE_ALERT_RULE_REQUEST_FAILURE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ALERT_RULE_REQUEST_FAILURE_SOURCE_VALUES!r}")
