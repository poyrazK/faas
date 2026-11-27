from typing import Literal

ManagedPostgresAutovacuumState = Literal["applied", "parked", "pending", "unavailable"]

MANAGED_POSTGRES_AUTOVACUUM_STATE_VALUES: set[ManagedPostgresAutovacuumState] = {
    "applied",
    "parked",
    "pending",
    "unavailable",
}


def check_managed_postgres_autovacuum_state(value: str) -> ManagedPostgresAutovacuumState:
    if value in MANAGED_POSTGRES_AUTOVACUUM_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_AUTOVACUUM_STATE_VALUES!r}")
