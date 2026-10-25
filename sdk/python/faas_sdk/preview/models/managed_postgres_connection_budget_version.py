from typing import Literal

ManagedPostgresConnectionBudgetVersion = Literal[1]

MANAGED_POSTGRES_CONNECTION_BUDGET_VERSION_VALUES: set[ManagedPostgresConnectionBudgetVersion] = {
    1,
}


def check_managed_postgres_connection_budget_version(value: int) -> ManagedPostgresConnectionBudgetVersion:
    if value in MANAGED_POSTGRES_CONNECTION_BUDGET_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CONNECTION_BUDGET_VERSION_VALUES!r}"
    )
