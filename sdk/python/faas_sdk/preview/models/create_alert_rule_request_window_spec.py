from typing import Literal

CreateAlertRuleRequestWindowSpec = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

CREATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES: set[CreateAlertRuleRequestWindowSpec] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_create_alert_rule_request_window_spec(value: str) -> CreateAlertRuleRequestWindowSpec:
    if value in CREATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES!r}")
