from typing import Literal

JobTaskAttemptResponseStatus = Literal["cancelled", "failed", "oom", "succeeded", "timeout"]

JOB_TASK_ATTEMPT_RESPONSE_STATUS_VALUES: set[JobTaskAttemptResponseStatus] = {
    "cancelled",
    "failed",
    "oom",
    "succeeded",
    "timeout",
}


def check_job_task_attempt_response_status(value: str) -> JobTaskAttemptResponseStatus:
    if value in JOB_TASK_ATTEMPT_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_TASK_ATTEMPT_RESPONSE_STATUS_VALUES!r}")
