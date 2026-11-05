from typing import Literal

UpdateAlertRuleRequestComparison = Literal["above_baseline", "below_baseline", "gt", "gte", "lt", "lte"]

UPDATE_ALERT_RULE_REQUEST_COMPARISON_VALUES: set[UpdateAlertRuleRequestComparison] = {
    "above_baseline",
    "below_baseline",
    "gt",
    "gte",
    "lt",
    "lte",
}


def check_update_alert_rule_request_comparison(value: str) -> UpdateAlertRuleRequestComparison:
    if value in UPDATE_ALERT_RULE_REQUEST_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_ALERT_RULE_REQUEST_COMPARISON_VALUES!r}")
