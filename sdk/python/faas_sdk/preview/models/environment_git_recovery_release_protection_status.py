from typing import Literal

EnvironmentGitRecoveryReleaseProtectionStatus = Literal[
    "budget_exceeded", "disabled", "expired", "outside_policy", "protected"
]

ENVIRONMENT_GIT_RECOVERY_RELEASE_PROTECTION_STATUS_VALUES: set[EnvironmentGitRecoveryReleaseProtectionStatus] = {
    "budget_exceeded",
    "disabled",
    "expired",
    "outside_policy",
    "protected",
}


def check_environment_git_recovery_release_protection_status(
    value: str,
) -> EnvironmentGitRecoveryReleaseProtectionStatus:
    if value in ENVIRONMENT_GIT_RECOVERY_RELEASE_PROTECTION_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_RECOVERY_RELEASE_PROTECTION_STATUS_VALUES!r}"
    )
