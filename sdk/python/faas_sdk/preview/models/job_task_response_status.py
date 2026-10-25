from typing import Literal

JobTaskResponseStatus = Literal["cancelled", "claimed", "failed", "oom", "queued", "succeeded", "timeout"]

JOB_TASK_RESPONSE_STATUS_VALUES: set[JobTaskResponseStatus] = {
    "cancelled",
    "claimed",
    "failed",
    "oom",
    "queued",
    "succeeded",
    "timeout",
}


def check_job_task_response_status(value: str) -> JobTaskResponseStatus:
    if value in JOB_TASK_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_TASK_RESPONSE_STATUS_VALUES!r}")
