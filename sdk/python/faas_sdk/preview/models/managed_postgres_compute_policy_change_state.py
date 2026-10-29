from typing import Literal

ManagedPostgresComputePolicyChangeState = Literal["pending", "succeeded"]

MANAGED_POSTGRES_COMPUTE_POLICY_CHANGE_STATE_VALUES: set[ManagedPostgresComputePolicyChangeState] = {
    "pending",
    "succeeded",
}


def check_managed_postgres_compute_policy_change_state(value: str) -> ManagedPostgresComputePolicyChangeState:
    if value in MANAGED_POSTGRES_COMPUTE_POLICY_CHANGE_STATE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_COMPUTE_POLICY_CHANGE_STATE_VALUES!r}"
    )
