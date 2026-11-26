from typing import Literal

SavedInsightAlertComparison = Literal["gt", "gte", "lt", "lte"]

SAVED_INSIGHT_ALERT_COMPARISON_VALUES: set[SavedInsightAlertComparison] = {
    "gt",
    "gte",
    "lt",
    "lte",
}


def check_saved_insight_alert_comparison(value: str) -> SavedInsightAlertComparison:
    if value in SAVED_INSIGHT_ALERT_COMPARISON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SAVED_INSIGHT_ALERT_COMPARISON_VALUES!r}")
