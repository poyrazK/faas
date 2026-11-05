from typing import Literal

SetEnvironmentRouteBudgetsScope = Literal["default"]

SET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES: set[SetEnvironmentRouteBudgetsScope] = {
    "default",
}


def check_set_environment_route_budgets_scope(value: str) -> SetEnvironmentRouteBudgetsScope:
    if value in SET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SET_ENVIRONMENT_ROUTE_BUDGETS_SCOPE_VALUES!r}")
