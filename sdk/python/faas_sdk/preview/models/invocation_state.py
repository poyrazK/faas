from typing import Literal

InvocationState = Literal["cancelled", "completed", "dead_letter", "dispatching", "failed", "pending"]

INVOCATION_STATE_VALUES: set[InvocationState] = {
    "cancelled",
    "completed",
    "dead_letter",
    "dispatching",
    "failed",
    "pending",
}


def check_invocation_state(value: str) -> InvocationState:
    if value in INVOCATION_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOCATION_STATE_VALUES!r}")
