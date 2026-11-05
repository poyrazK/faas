from typing import Literal

ControlRealtimeReplayJobAction = Literal["cancel", "pause", "resume", "start"]

CONTROL_REALTIME_REPLAY_JOB_ACTION_VALUES: set[ControlRealtimeReplayJobAction] = {
    "cancel",
    "pause",
    "resume",
    "start",
}


def check_control_realtime_replay_job_action(value: str) -> ControlRealtimeReplayJobAction:
    if value in CONTROL_REALTIME_REPLAY_JOB_ACTION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONTROL_REALTIME_REPLAY_JOB_ACTION_VALUES!r}")
