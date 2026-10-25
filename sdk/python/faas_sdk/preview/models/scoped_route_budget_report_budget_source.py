from typing import Literal

ScopedRouteBudgetReportBudgetSource = Literal["app_policy", "environment_policy"]

SCOPED_ROUTE_BUDGET_REPORT_BUDGET_SOURCE_VALUES: set[ScopedRouteBudgetReportBudgetSource] = {
    "app_policy",
    "environment_policy",
}


def check_scoped_route_budget_report_budget_source(value: str) -> ScopedRouteBudgetReportBudgetSource:
    if value in SCOPED_ROUTE_BUDGET_REPORT_BUDGET_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SCOPED_ROUTE_BUDGET_REPORT_BUDGET_SOURCE_VALUES!r}")
