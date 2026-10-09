from typing import Literal

EventRecoveryRequestMode = Literal["execution", "routing"]

EVENT_RECOVERY_REQUEST_MODE_VALUES: set[EventRecoveryRequestMode] = {
    "execution",
    "routing",
}


def check_event_recovery_request_mode(value: str) -> EventRecoveryRequestMode:
    if value in EVENT_RECOVERY_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EVENT_RECOVERY_REQUEST_MODE_VALUES!r}")
