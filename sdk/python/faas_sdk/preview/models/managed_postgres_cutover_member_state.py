from typing import Literal

ManagedPostgresCutoverMemberState = Literal["pending", "revoked", "sealed"]

MANAGED_POSTGRES_CUTOVER_MEMBER_STATE_VALUES: set[ManagedPostgresCutoverMemberState] = {
    "pending",
    "revoked",
    "sealed",
}


def check_managed_postgres_cutover_member_state(value: str) -> ManagedPostgresCutoverMemberState:
    if value in MANAGED_POSTGRES_CUTOVER_MEMBER_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_CUTOVER_MEMBER_STATE_VALUES!r}")
