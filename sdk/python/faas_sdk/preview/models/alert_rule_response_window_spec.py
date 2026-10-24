from typing import Literal

AlertRuleResponseWindowSpec = Literal["15d", "15m", "1h", "24h", "5m", "6h", "7d"]

ALERT_RULE_RESPONSE_WINDOW_SPEC_VALUES: set[AlertRuleResponseWindowSpec] = {
    "15d",
    "15m",
    "1h",
    "24h",
    "5m",
    "6h",
    "7d",
}


def check_alert_rule_response_window_spec(value: str) -> AlertRuleResponseWindowSpec:
    if value in ALERT_RULE_RESPONSE_WINDOW_SPEC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ALERT_RULE_RESPONSE_WINDOW_SPEC_VALUES!r}")
