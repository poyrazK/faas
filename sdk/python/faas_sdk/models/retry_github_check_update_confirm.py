from typing import Literal

RetryGithubCheckUpdateConfirm = Literal["true"]

RETRY_GITHUB_CHECK_UPDATE_CONFIRM_VALUES: set[RetryGithubCheckUpdateConfirm] = {
    "true",
}


def check_retry_github_check_update_confirm(value: str) -> RetryGithubCheckUpdateConfirm:
    if value in RETRY_GITHUB_CHECK_UPDATE_CONFIRM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {RETRY_GITHUB_CHECK_UPDATE_CONFIRM_VALUES!r}")
