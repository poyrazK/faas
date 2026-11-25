from typing import Literal

ManagedPostgresAutovacuumProfile = Literal["default", "high-churn"]

MANAGED_POSTGRES_AUTOVACUUM_PROFILE_VALUES: set[ManagedPostgresAutovacuumProfile] = {
    "default",
    "high-churn",
}


def check_managed_postgres_autovacuum_profile(value: str) -> ManagedPostgresAutovacuumProfile:
    if value in MANAGED_POSTGRES_AUTOVACUUM_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_AUTOVACUUM_PROFILE_VALUES!r}")
