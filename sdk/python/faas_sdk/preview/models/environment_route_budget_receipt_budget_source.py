from typing import Literal

EnvironmentRouteBudgetReceiptBudgetSource = Literal["app_policy", "environment_policy"]

ENVIRONMENT_ROUTE_BUDGET_RECEIPT_BUDGET_SOURCE_VALUES: set[EnvironmentRouteBudgetReceiptBudgetSource] = {
    "app_policy",
    "environment_policy",
}


def check_environment_route_budget_receipt_budget_source(value: str) -> EnvironmentRouteBudgetReceiptBudgetSource:
    if value in ENVIRONMENT_ROUTE_BUDGET_RECEIPT_BUDGET_SOURCE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_ROUTE_BUDGET_RECEIPT_BUDGET_SOURCE_VALUES!r}"
    )
