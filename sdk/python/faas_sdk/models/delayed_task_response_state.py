from typing import Literal

DelayedTaskResponseState = Literal["cancelled", "completed", "dispatching", "failed", "pending"]

DELAYED_TASK_RESPONSE_STATE_VALUES: set[DelayedTaskResponseState] = {
    "cancelled",
    "completed",
    "dispatching",
    "failed",
    "pending",
}


def check_delayed_task_response_state(value: str) -> DelayedTaskResponseState:
    if value in DELAYED_TASK_RESPONSE_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DELAYED_TASK_RESPONSE_STATE_VALUES!r}")
