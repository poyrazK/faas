from typing import Literal

RealtimeReplayJobState = Literal["cancelled", "check_paused", "checking", "completed", "paused", "running"]

REALTIME_REPLAY_JOB_STATE_VALUES: set[RealtimeReplayJobState] = {
    "cancelled",
    "check_paused",
    "checking",
    "completed",
    "paused",
    "running",
}


def check_realtime_replay_job_state(value: str) -> RealtimeReplayJobState:
    if value in REALTIME_REPLAY_JOB_STATE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_REPLAY_JOB_STATE_VALUES!r}")
