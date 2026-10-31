from typing import Literal

JobTaskResponseErrorClass = Literal["cancelled", "failed", "infra", "oom", "succeeded", "timeout"]

JOB_TASK_RESPONSE_ERROR_CLASS_VALUES: set[JobTaskResponseErrorClass] = {
    "cancelled",
    "failed",
    "infra",
    "oom",
    "succeeded",
    "timeout",
}


def check_job_task_response_error_class(value: str) -> JobTaskResponseErrorClass:
    if value in JOB_TASK_RESPONSE_ERROR_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_TASK_RESPONSE_ERROR_CLASS_VALUES!r}")
