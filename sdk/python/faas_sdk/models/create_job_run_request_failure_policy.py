from typing import Literal

CreateJobRunRequestFailurePolicy = Literal["continue", "fail_fast"]

CREATE_JOB_RUN_REQUEST_FAILURE_POLICY_VALUES: set[CreateJobRunRequestFailurePolicy] = {
    "continue",
    "fail_fast",
}


def check_create_job_run_request_failure_policy(value: str) -> CreateJobRunRequestFailurePolicy:
    if value in CREATE_JOB_RUN_REQUEST_FAILURE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_JOB_RUN_REQUEST_FAILURE_POLICY_VALUES!r}")
