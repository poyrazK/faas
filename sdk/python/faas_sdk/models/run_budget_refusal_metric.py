from typing import Literal

RunBudgetRefusalMetric = Literal["compute_mb_ms", "concurrent", "enabled", "invocations"]

RUN_BUDGET_REFUSAL_METRIC_VALUES: set[RunBudgetRefusalMetric] = {
    "compute_mb_ms",
    "concurrent",
    "enabled",
    "invocations",
}


def check_run_budget_refusal_metric(value: str) -> RunBudgetRefusalMetric:
    if value in RUN_BUDGET_REFUSAL_METRIC_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RUN_BUDGET_REFUSAL_METRIC_VALUES!r}")
