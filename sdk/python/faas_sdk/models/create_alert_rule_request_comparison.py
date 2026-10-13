from typing import Literal

CreateAlertRuleRequestComparison = Literal["above_baseline", "below_baseline", "gt", "gte", "lt", "lte"]

CREATE_ALERT_RULE_REQUEST_COMPARISON_VALUES: set[CreateAlertRuleRequestComparison] = {
    "above_baseline",
    "below_baseline",
    "gt",
    "gte",
    "lt",
    "lte",
}


def check_create_alert_rule_request_comparison(value: str) -> CreateAlertRuleRequestComparison:
    if value in CREATE_ALERT_RULE_REQUEST_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_ALERT_RULE_REQUEST_COMPARISON_VALUES!r}")
