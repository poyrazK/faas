from typing import Literal

UpdateAlertRuleRequestWindowSpec = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

UPDATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES: set[UpdateAlertRuleRequestWindowSpec] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_update_alert_rule_request_window_spec(value: str) -> UpdateAlertRuleRequestWindowSpec:
    if value in UPDATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_ALERT_RULE_REQUEST_WINDOW_SPEC_VALUES!r}")
