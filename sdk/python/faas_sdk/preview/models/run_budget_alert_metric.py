from typing import Literal

RunBudgetAlertMetric = Literal["compute_mb_ms", "concurrent", "invocations"]

RUN_BUDGET_ALERT_METRIC_VALUES: set[RunBudgetAlertMetric] = {
    "compute_mb_ms",
    "concurrent",
    "invocations",
}


def check_run_budget_alert_metric(value: str) -> RunBudgetAlertMetric:
    if value in RUN_BUDGET_ALERT_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_BUDGET_ALERT_METRIC_VALUES!r}")
