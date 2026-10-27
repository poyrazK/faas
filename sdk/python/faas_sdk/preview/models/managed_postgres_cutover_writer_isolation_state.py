from typing import Literal

ManagedPostgresCutoverWriterIsolationState = Literal[
    "blocked", "confirmed", "expired", "inactive", "pending", "unavailable", "unsupported"
]

MANAGED_POSTGRES_CUTOVER_WRITER_ISOLATION_STATE_VALUES: set[ManagedPostgresCutoverWriterIsolationState] = {
    "blocked",
    "confirmed",
    "expired",
    "inactive",
    "pending",
    "unavailable",
    "unsupported",
}


def check_managed_postgres_cutover_writer_isolation_state(value: str) -> ManagedPostgresCutoverWriterIsolationState:
    if value in MANAGED_POSTGRES_CUTOVER_WRITER_ISOLATION_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_WRITER_ISOLATION_STATE_VALUES!r}"
    )
