from typing import Literal

EnvironmentRouteBudgetsBudgetSource = Literal["app_policy", "environment_policy"]

ENVIRONMENT_ROUTE_BUDGETS_BUDGET_SOURCE_VALUES: set[EnvironmentRouteBudgetsBudgetSource] = {
    "app_policy",
    "environment_policy",
}


def check_environment_route_budgets_budget_source(value: str) -> EnvironmentRouteBudgetsBudgetSource:
    if value in ENVIRONMENT_ROUTE_BUDGETS_BUDGET_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_ROUTE_BUDGETS_BUDGET_SOURCE_VALUES!r}")
