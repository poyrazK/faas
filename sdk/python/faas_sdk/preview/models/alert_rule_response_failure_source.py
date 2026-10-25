from typing import Literal

AlertRuleResponseFailureSource = Literal["any", "async_invoke", "cron", "delayed_task", "inbound_webhook", "queue"]

ALERT_RULE_RESPONSE_FAILURE_SOURCE_VALUES: set[AlertRuleResponseFailureSource] = {
    "any",
    "async_invoke",
    "cron",
    "delayed_task",
    "inbound_webhook",
    "queue",
}


def check_alert_rule_response_failure_source(value: str) -> AlertRuleResponseFailureSource:
    if value in ALERT_RULE_RESPONSE_FAILURE_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_FAILURE_SOURCE_VALUES!r}")
