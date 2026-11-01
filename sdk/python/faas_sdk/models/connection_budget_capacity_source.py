from typing import Literal

ConnectionBudgetCapacitySource = Literal["native_qualification", "provider_api"]

CONNECTION_BUDGET_CAPACITY_SOURCE_VALUES: set[ConnectionBudgetCapacitySource] = {
    "native_qualification",
    "provider_api",
}


def check_connection_budget_capacity_source(value: str) -> ConnectionBudgetCapacitySource:
    if value in CONNECTION_BUDGET_CAPACITY_SOURCE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONNECTION_BUDGET_CAPACITY_SOURCE_VALUES!r}")
