from typing import Literal

ManagedPostgresConnectionBudgetState = Literal["over_budget", "unknown", "within_budget"]

MANAGED_POSTGRES_CONNECTION_BUDGET_STATE_VALUES: set[ManagedPostgresConnectionBudgetState] = {
    "over_budget",
    "unknown",
    "within_budget",
}


def check_managed_postgres_connection_budget_state(value: str) -> ManagedPostgresConnectionBudgetState:
    if value in MANAGED_POSTGRES_CONNECTION_BUDGET_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CONNECTION_BUDGET_STATE_VALUES!r}")
