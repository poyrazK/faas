from typing import Literal

GetEnvironmentRouteBudgetReceiptScope = Literal["default"]

GET_ENVIRONMENT_ROUTE_BUDGET_RECEIPT_SCOPE_VALUES: set[GetEnvironmentRouteBudgetReceiptScope] = {
    "default",
}


def check_get_environment_route_budget_receipt_scope(value: str) -> GetEnvironmentRouteBudgetReceiptScope:
    if value in GET_ENVIRONMENT_ROUTE_BUDGET_RECEIPT_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GET_ENVIRONMENT_ROUTE_BUDGET_RECEIPT_SCOPE_VALUES!r}"
    )
