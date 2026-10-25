from typing import Literal

GithubCheckUpdateRecordStatus = Literal["dead", "pending", "processing", "succeeded"]

GITHUB_CHECK_UPDATE_RECORD_STATUS_VALUES: set[GithubCheckUpdateRecordStatus] = {
    "dead",
    "pending",
    "processing",
    "succeeded",
}


def check_github_check_update_record_status(value: str) -> GithubCheckUpdateRecordStatus:
    if value in GITHUB_CHECK_UPDATE_RECORD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GITHUB_CHECK_UPDATE_RECORD_STATUS_VALUES!r}")
