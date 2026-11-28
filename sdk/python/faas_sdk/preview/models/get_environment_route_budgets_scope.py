from typing import Literal

GetEnvironmentRouteBudgetsScope = Literal["default"]

GET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES: set[GetEnvironmentRouteBudgetsScope] = {
    "default",
}


def check_get_environment_route_budgets_scope(value: str) -> GetEnvironmentRouteBudgetsScope:
    if value in GET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES!r}")
