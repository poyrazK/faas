from typing import Literal

JobResponseStatus = Literal["active", "deleted", "paused"]

JOB_RESPONSE_STATUS_VALUES: set[JobResponseStatus] = {
    "active",
    "deleted",
    "paused",
}


def check_job_response_status(value: str) -> JobResponseStatus:
    if value in JOB_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RESPONSE_STATUS_VALUES!r}")
