from typing import Literal

JobRunResponseExecutionClass = Literal["flexible", "standard"]

JOB_RUN_RESPONSE_EXECUTION_CLASS_VALUES: set[JobRunResponseExecutionClass] = {
    "flexible",
    "standard",
}


def check_job_run_response_execution_class(value: str) -> JobRunResponseExecutionClass:
    if value in JOB_RUN_RESPONSE_EXECUTION_CLASS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RUN_RESPONSE_EXECUTION_CLASS_VALUES!r}")
