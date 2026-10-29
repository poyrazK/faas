from typing import Literal

JobTaskLogResponseTaskStatus = Literal["cancelled", "claimed", "failed", "oom", "queued", "succeeded", "timeout"]

JOB_TASK_LOG_RESPONSE_TASK_STATUS_VALUES: set[JobTaskLogResponseTaskStatus] = {
    "cancelled",
    "claimed",
    "failed",
    "oom",
    "queued",
    "succeeded",
    "timeout",
}


def check_job_task_log_response_task_status(value: str) -> JobTaskLogResponseTaskStatus:
    if value in JOB_TASK_LOG_RESPONSE_TASK_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_TASK_LOG_RESPONSE_TASK_STATUS_VALUES!r}")
