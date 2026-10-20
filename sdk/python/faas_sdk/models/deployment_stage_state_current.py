from typing import Literal

DeploymentStageStateCurrent = Literal[
    "", "dependency_restore", "image_build", "readiness", "security_scan", "snapshot_prepare", "source_download"
]

DEPLOYMENT_STAGE_STATE_CURRENT_VALUES: set[DeploymentStageStateCurrent] = {
    "",
    "dependency_restore",
    "image_build",
    "readiness",
    "security_scan",
    "snapshot_prepare",
    "source_download",
}


def check_deployment_stage_state_current(value: str) -> DeploymentStageStateCurrent:
    if value in DEPLOYMENT_STAGE_STATE_CURRENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGE_STATE_CURRENT_VALUES!r}")
