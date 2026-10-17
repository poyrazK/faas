from typing import Literal

ManagedPostgresMajorUpgradeState = Literal["failed", "pending", "succeeded"]

MANAGED_POSTGRES_MAJOR_UPGRADE_STATE_VALUES: set[ManagedPostgresMajorUpgradeState] = {
    "failed",
    "pending",
    "succeeded",
}


def check_managed_postgres_major_upgrade_state(value: str) -> ManagedPostgresMajorUpgradeState:
    if value in MANAGED_POSTGRES_MAJOR_UPGRADE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MANAGED_POSTGRES_MAJOR_UPGRADE_STATE_VALUES!r}")
