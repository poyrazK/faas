from typing import Literal

JobRunResponseFailurePolicy = Literal["continue", "fail_fast"]

JOB_RUN_RESPONSE_FAILURE_POLICY_VALUES: set[JobRunResponseFailurePolicy] = {
    "continue",
    "fail_fast",
}


def check_job_run_response_failure_policy(value: str) -> JobRunResponseFailurePolicy:
    if value in JOB_RUN_RESPONSE_FAILURE_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RUN_RESPONSE_FAILURE_POLICY_VALUES!r}")
