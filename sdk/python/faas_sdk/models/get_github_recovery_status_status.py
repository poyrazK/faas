from typing import Literal

GetGithubRecoveryStatusStatus = Literal["dead", "pending", "processing", "succeeded"]

GET_GITHUB_RECOVERY_STATUS_STATUS_VALUES: set[GetGithubRecoveryStatusStatus] = {
    "dead",
    "pending",
    "processing",
    "succeeded",
}


def check_get_github_recovery_status_status(value: str) -> GetGithubRecoveryStatusStatus:
    if value in GET_GITHUB_RECOVERY_STATUS_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GET_GITHUB_RECOVERY_STATUS_STATUS_VALUES!r}")
