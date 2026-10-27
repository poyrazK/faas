from typing import Literal

PostSweepStuckBuildsConfirm = Literal["true"]

POST_SWEEP_STUCK_BUILDS_CONFIRM_VALUES: set[PostSweepStuckBuildsConfirm] = {
    "true",
}


def check_post_sweep_stuck_builds_confirm(value: str) -> PostSweepStuckBuildsConfirm:
    if value in POST_SWEEP_STUCK_BUILDS_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {POST_SWEEP_STUCK_BUILDS_CONFIRM_VALUES!r}")
