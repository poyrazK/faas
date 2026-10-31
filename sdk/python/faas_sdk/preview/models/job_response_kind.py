from typing import Literal

JobResponseKind = Literal["batch", "image_run", "recurring"]

JOB_RESPONSE_KIND_VALUES: set[JobResponseKind] = {
    "batch",
    "image_run",
    "recurring",
}


def check_job_response_kind(value: str) -> JobResponseKind:
    if value in JOB_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RESPONSE_KIND_VALUES!r}")
