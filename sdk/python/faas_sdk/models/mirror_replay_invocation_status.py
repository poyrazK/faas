from typing import Literal

MirrorReplayInvocationStatus = Literal["queued"]

MIRROR_REPLAY_INVOCATION_STATUS_VALUES: set[MirrorReplayInvocationStatus] = {
    "queued",
}


def check_mirror_replay_invocation_status(value: str) -> MirrorReplayInvocationStatus:
    if value in MIRROR_REPLAY_INVOCATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIRROR_REPLAY_INVOCATION_STATUS_VALUES!r}")
