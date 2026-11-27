from typing import Literal

SetManagedPostgresAutovacuumRequestProfile = Literal["default", "high-churn"]

SET_MANAGED_POSTGRES_AUTOVACUUM_REQUEST_PROFILE_VALUES: set[SetManagedPostgresAutovacuumRequestProfile] = {
    "default",
    "high-churn",
}


def check_set_managed_postgres_autovacuum_request_profile(value: str) -> SetManagedPostgresAutovacuumRequestProfile:
    if value in SET_MANAGED_POSTGRES_AUTOVACUUM_REQUEST_PROFILE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SET_MANAGED_POSTGRES_AUTOVACUUM_REQUEST_PROFILE_VALUES!r}"
    )
