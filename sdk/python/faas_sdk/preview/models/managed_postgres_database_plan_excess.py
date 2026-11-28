from typing import Literal

ManagedPostgresDatabasePlanExcess = Literal["database_limit", "service_class"]

MANAGED_POSTGRES_DATABASE_PLAN_EXCESS_VALUES: set[ManagedPostgresDatabasePlanExcess] = {
    "database_limit",
    "service_class",
}


def check_managed_postgres_database_plan_excess(value: str) -> ManagedPostgresDatabasePlanExcess:
    if value in MANAGED_POSTGRES_DATABASE_PLAN_EXCESS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DATABASE_PLAN_EXCESS_VALUES!r}")
