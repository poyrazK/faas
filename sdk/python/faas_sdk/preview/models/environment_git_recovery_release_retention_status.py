from typing import Literal

EnvironmentGitRecoveryReleaseRetentionStatus = Literal[
    "active", "compatibility_window", "compatibility_window_elapsed", "unknown"
]

ENVIRONMENT_GIT_RECOVERY_RELEASE_RETENTION_STATUS_VALUES: set[EnvironmentGitRecoveryReleaseRetentionStatus] = {
    "active",
    "compatibility_window",
    "compatibility_window_elapsed",
    "unknown",
}


def check_environment_git_recovery_release_retention_status(value: str) -> EnvironmentGitRecoveryReleaseRetentionStatus:
    if value in ENVIRONMENT_GIT_RECOVERY_RELEASE_RETENTION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_RECOVERY_RELEASE_RETENTION_STATUS_VALUES!r}"
    )
