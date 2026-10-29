from typing import Literal

DeploymentStageStateRetryRequestedStage = Literal[
    "dependency_restore", "image_build", "readiness", "security_scan", "snapshot_prepare", "source_download"
]

DEPLOYMENT_STAGE_STATE_RETRY_REQUESTED_STAGE_VALUES: set[DeploymentStageStateRetryRequestedStage] = {
    "dependency_restore",
    "image_build",
    "readiness",
    "security_scan",
    "snapshot_prepare",
    "source_download",
}


def check_deployment_stage_state_retry_requested_stage(value: str) -> DeploymentStageStateRetryRequestedStage:
    if value in DEPLOYMENT_STAGE_STATE_RETRY_REQUESTED_STAGE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGE_STATE_RETRY_REQUESTED_STAGE_VALUES!r}"
    )
