from typing import Literal

GitHubCheckActivityStatus = Literal["dead", "pending", "processing", "succeeded"]

GIT_HUB_CHECK_ACTIVITY_STATUS_VALUES: set[GitHubCheckActivityStatus] = {
    "dead",
    "pending",
    "processing",
    "succeeded",
}


def check_git_hub_check_activity_status(value: str) -> GitHubCheckActivityStatus:
    if value in GIT_HUB_CHECK_ACTIVITY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_CHECK_ACTIVITY_STATUS_VALUES!r}")
