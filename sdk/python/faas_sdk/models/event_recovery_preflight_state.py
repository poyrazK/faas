from typing import Literal

EventRecoveryPreflightState = Literal["cancelled", "completed", "paused", "running"]

EVENT_RECOVERY_PREFLIGHT_STATE_VALUES: set[EventRecoveryPreflightState] = {
    "cancelled",
    "completed",
    "paused",
    "running",
}


def check_event_recovery_preflight_state(value: str) -> EventRecoveryPreflightState:
    if value in EVENT_RECOVERY_PREFLIGHT_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_PREFLIGHT_STATE_VALUES!r}")
