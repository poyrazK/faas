from typing import Literal

CreateJobRequestKind = Literal["batch", "recurring"]

CREATE_JOB_REQUEST_KIND_VALUES: set[CreateJobRequestKind] = {
    "batch",
    "recurring",
}


def check_create_job_request_kind(value: str) -> CreateJobRequestKind:
    if value in CREATE_JOB_REQUEST_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_JOB_REQUEST_KIND_VALUES!r}")
