from typing import Literal

RetryDeploymentRequestFromStage = Literal[
    "dependency_restore", "image_build", "readiness", "security_scan", "snapshot_prepare", "source_download"
]

RETRY_DEPLOYMENT_REQUEST_FROM_STAGE_VALUES: set[RetryDeploymentRequestFromStage] = {
    "dependency_restore",
    "image_build",
    "readiness",
    "security_scan",
    "snapshot_prepare",
    "source_download",
}


def check_retry_deployment_request_from_stage(value: str) -> RetryDeploymentRequestFromStage:
    if value in RETRY_DEPLOYMENT_REQUEST_FROM_STAGE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RETRY_DEPLOYMENT_REQUEST_FROM_STAGE_VALUES!r}")
