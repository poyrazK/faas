from typing import Literal

DeploymentStageHistoryEntryStatus = Literal["cancelled", "completed", "failed"]

DEPLOYMENT_STAGE_HISTORY_ENTRY_STATUS_VALUES: set[DeploymentStageHistoryEntryStatus] = {
    "cancelled",
    "completed",
    "failed",
}


def check_deployment_stage_history_entry_status(value: str) -> DeploymentStageHistoryEntryStatus:
    if value in DEPLOYMENT_STAGE_HISTORY_ENTRY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_STAGE_HISTORY_ENTRY_STATUS_VALUES!r}")
