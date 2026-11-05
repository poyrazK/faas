from typing import Literal

ManagedPostgresCutoverRuntimeDrainMemberState = Literal["blocked", "pending", "retired"]

MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_MEMBER_STATE_VALUES: set[ManagedPostgresCutoverRuntimeDrainMemberState] = {
    "blocked",
    "pending",
    "retired",
}


def check_managed_postgres_cutover_runtime_drain_member_state(
    value: str,
) -> ManagedPostgresCutoverRuntimeDrainMemberState:
    if value in MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_MEMBER_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_RUNTIME_DRAIN_MEMBER_STATE_VALUES!r}"
    )
