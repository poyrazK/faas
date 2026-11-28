from typing import Literal

RunBudgetAlertThresholdPercent = Literal[80, 100]

RUN_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES: set[RunBudgetAlertThresholdPercent] = {
    80,
    100,
}


def check_run_budget_alert_threshold_percent(value: int) -> RunBudgetAlertThresholdPercent:
    if value in RUN_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_BUDGET_ALERT_THRESHOLD_PERCENT_VALUES!r}")
