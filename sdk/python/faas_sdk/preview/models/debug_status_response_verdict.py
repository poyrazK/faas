from typing import Literal

DebugStatusResponseVerdict = Literal["degraded", "failing", "healthy", "idle", "releasing", "rolled_back"]

DEBUG_STATUS_RESPONSE_VERDICT_VALUES: set[DebugStatusResponseVerdict] = {
    "degraded",
    "failing",
    "healthy",
    "idle",
    "releasing",
    "rolled_back",
}


def check_debug_status_response_verdict(value: str) -> DebugStatusResponseVerdict:
    if value in DEBUG_STATUS_RESPONSE_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_STATUS_RESPONSE_VERDICT_VALUES!r}")
