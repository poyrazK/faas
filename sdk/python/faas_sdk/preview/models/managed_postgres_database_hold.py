from typing import Literal

ManagedPostgresDatabaseHold = Literal["compute_ceiling", "egress", "inactive", "plan_excess", "preview"]

MANAGED_POSTGRES_DATABASE_HOLD_VALUES: set[ManagedPostgresDatabaseHold] = {
    "compute_ceiling",
    "egress",
    "inactive",
    "plan_excess",
    "preview",
}


def check_managed_postgres_database_hold(value: str) -> ManagedPostgresDatabaseHold:
    if value in MANAGED_POSTGRES_DATABASE_HOLD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DATABASE_HOLD_VALUES!r}")
