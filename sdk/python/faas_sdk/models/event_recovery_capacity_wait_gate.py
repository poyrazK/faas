from typing import Literal

EventRecoveryCapacityWaitGate = Literal["pending_delivery_limit", "unknown"]

EVENT_RECOVERY_CAPACITY_WAIT_GATE_VALUES: set[EventRecoveryCapacityWaitGate] = {
    "pending_delivery_limit",
    "unknown",
}


def check_event_recovery_capacity_wait_gate(value: str) -> EventRecoveryCapacityWaitGate:
    if value in EVENT_RECOVERY_CAPACITY_WAIT_GATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_CAPACITY_WAIT_GATE_VALUES!r}")
