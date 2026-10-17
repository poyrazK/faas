from typing import Literal

DeploymentStageHistoryEntryName = Literal[
    "dependency_restore", "image_build", "readiness", "security_scan", "snapshot_prepare", "source_download"
]

DEPLOYMENT_STAGE_HISTORY_ENTRY_NAME_VALUES: set[DeploymentStageHistoryEntryName] = {
    "dependency_restore",
    "image_build",
    "readiness",
    "security_scan",
    "snapshot_prepare",
    "source_download",
}


def check_deployment_stage_history_entry_name(value: str) -> DeploymentStageHistoryEntryName:
    if value in DEPLOYMENT_STAGE_HISTORY_ENTRY_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGE_HISTORY_ENTRY_NAME_VALUES!r}")
