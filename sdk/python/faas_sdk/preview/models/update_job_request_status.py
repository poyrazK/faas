from typing import Literal

UpdateJobRequestStatus = Literal["active", "paused"]

UPDATE_JOB_REQUEST_STATUS_VALUES: set[UpdateJobRequestStatus] = {
    "active",
    "paused",
}


def check_update_job_request_status(value: str) -> UpdateJobRequestStatus:
    if value in UPDATE_JOB_REQUEST_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_JOB_REQUEST_STATUS_VALUES!r}")
