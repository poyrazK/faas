from typing import Literal

CreateJobRunRequestExecutionClass = Literal["flexible", "standard"]

CREATE_JOB_RUN_REQUEST_EXECUTION_CLASS_VALUES: set[CreateJobRunRequestExecutionClass] = {
    "flexible",
    "standard",
}


def check_create_job_run_request_execution_class(value: str) -> CreateJobRunRequestExecutionClass:
    if value in CREATE_JOB_RUN_REQUEST_EXECUTION_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_JOB_RUN_REQUEST_EXECUTION_CLASS_VALUES!r}")
