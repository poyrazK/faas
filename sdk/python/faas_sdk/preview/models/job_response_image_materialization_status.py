from typing import Literal

JobResponseImageMaterializationStatus = Literal["failed", "pending", "ready", "verifying_legacy"]

JOB_RESPONSE_IMAGE_MATERIALIZATION_STATUS_VALUES: set[JobResponseImageMaterializationStatus] = {
    "failed",
    "pending",
    "ready",
    "verifying_legacy",
}


def check_job_response_image_materialization_status(value: str) -> JobResponseImageMaterializationStatus:
    if value in JOB_RESPONSE_IMAGE_MATERIALIZATION_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_RESPONSE_IMAGE_MATERIALIZATION_STATUS_VALUES!r}")
