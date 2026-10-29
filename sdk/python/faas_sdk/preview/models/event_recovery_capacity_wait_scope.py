from typing import Literal

EventRecoveryCapacityWaitScope = Literal["account", "app", "consumer", "unknown"]

EVENT_RECOVERY_CAPACITY_WAIT_SCOPE_VALUES: set[EventRecoveryCapacityWaitScope] = {
    "account",
    "app",
    "consumer",
    "unknown",
}


def check_event_recovery_capacity_wait_scope(value: str) -> EventRecoveryCapacityWaitScope:
    if value in EVENT_RECOVERY_CAPACITY_WAIT_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_CAPACITY_WAIT_SCOPE_VALUES!r}")
