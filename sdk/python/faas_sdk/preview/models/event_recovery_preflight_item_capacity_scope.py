from typing import Literal

EventRecoveryPreflightItemCapacityScope = Literal["account", "app", "consumer"]

EVENT_RECOVERY_PREFLIGHT_ITEM_CAPACITY_SCOPE_VALUES: set[EventRecoveryPreflightItemCapacityScope] = {
    "account",
    "app",
    "consumer",
}


def check_event_recovery_preflight_item_capacity_scope(value: str) -> EventRecoveryPreflightItemCapacityScope:
    if value in EVENT_RECOVERY_PREFLIGHT_ITEM_CAPACITY_SCOPE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_ITEM_CAPACITY_SCOPE_VALUES!r}"
    )
