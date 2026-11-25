from typing import Literal

ManagedPostgresDiagnosticsState = Literal["available", "parked", "unavailable"]

MANAGED_POSTGRES_DIAGNOSTICS_STATE_VALUES: set[ManagedPostgresDiagnosticsState] = {
    "available",
    "parked",
    "unavailable",
}


def check_managed_postgres_diagnostics_state(value: str) -> ManagedPostgresDiagnosticsState:
    if value in MANAGED_POSTGRES_DIAGNOSTICS_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_DIAGNOSTICS_STATE_VALUES!r}")
