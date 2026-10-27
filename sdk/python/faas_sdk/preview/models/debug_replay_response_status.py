from typing import Literal

DebugReplayResponseStatus = Literal["completed", "failed", "queued", "running"]

DEBUG_REPLAY_RESPONSE_STATUS_VALUES: set[DebugReplayResponseStatus] = {
    "completed",
    "failed",
    "queued",
    "running",
}


def check_debug_replay_response_status(value: str) -> DebugReplayResponseStatus:
    if value in DEBUG_REPLAY_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REPLAY_RESPONSE_STATUS_VALUES!r}")
