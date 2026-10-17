from typing import Literal

ManagedPostgresConnectionBudgetModel = Literal["canonical_runtime_pools", "qualified_shared_connection_limits"]

MANAGED_POSTGRES_CONNECTION_BUDGET_MODEL_VALUES: set[ManagedPostgresConnectionBudgetModel] = {
    "canonical_runtime_pools",
    "qualified_shared_connection_limits",
}


def check_managed_postgres_connection_budget_model(value: str) -> ManagedPostgresConnectionBudgetModel:
    if value in MANAGED_POSTGRES_CONNECTION_BUDGET_MODEL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CONNECTION_BUDGET_MODEL_VALUES!r}")
