from typing import Literal

ReleaseOutcomeCheckTaskStatus = Literal["cancelled", "failed", "timed_out"]

RELEASE_OUTCOME_CHECK_TASK_STATUS_VALUES: set[ReleaseOutcomeCheckTaskStatus] = {
    "cancelled",
    "failed",
    "timed_out",
}


def check_release_outcome_check_task_status(value: str) -> ReleaseOutcomeCheckTaskStatus:
    if value in RELEASE_OUTCOME_CHECK_TASK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RELEASE_OUTCOME_CHECK_TASK_STATUS_VALUES!r}")
