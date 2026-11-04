from typing import Literal

InvokeResponseStatus = Literal["cancelled", "completed", "dispatching", "failed", "pending"]

INVOKE_RESPONSE_STATUS_VALUES: set[InvokeResponseStatus] = {
    "cancelled",
    "completed",
    "dispatching",
    "failed",
    "pending",
}


def check_invoke_response_status(value: str) -> InvokeResponseStatus:
    if value in INVOKE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INVOKE_RESPONSE_STATUS_VALUES!r}")
