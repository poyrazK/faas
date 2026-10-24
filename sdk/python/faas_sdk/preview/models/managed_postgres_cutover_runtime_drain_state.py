from typing import Literal

ManagedPostgresCutoverRuntimeDrainState = Literal[
    "blocked", "catalog_retired", "draining", "missing", "not_fenced", "unavailable"
]

MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_STATE_VALUES: set[ManagedPostgresCutoverRuntimeDrainState] = {
    "blocked",
    "catalog_retired",
    "draining",
    "missing",
    "not_fenced",
    "unavailable",
}


def check_managed_postgres_cutover_runtime_drain_state(value: str) -> ManagedPostgresCutoverRuntimeDrainState:
    if value in MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_STATE_VALUES!r}"
    )
