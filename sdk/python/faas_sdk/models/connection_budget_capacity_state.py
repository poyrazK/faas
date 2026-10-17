from typing import Literal

ConnectionBudgetCapacityState = Literal["unknown", "verified"]

CONNECTION_BUDGET_CAPACITY_STATE_VALUES: set[ConnectionBudgetCapacityState] = {
    "unknown",
    "verified",
}


def check_connection_budget_capacity_state(value: str) -> ConnectionBudgetCapacityState:
    if value in CONNECTION_BUDGET_CAPACITY_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONNECTION_BUDGET_CAPACITY_STATE_VALUES!r}")
