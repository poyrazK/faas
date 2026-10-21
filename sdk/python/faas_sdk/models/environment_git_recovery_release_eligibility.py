from typing import Literal

EnvironmentGitRecoveryReleaseEligibility = Literal["blocked", "eligible"]

ENVIRONMENT_GIT_RECOVERY_RELEASE_ELIGIBILITY_VALUES: set[EnvironmentGitRecoveryReleaseEligibility] = {
    "blocked",
    "eligible",
}


def check_environment_git_recovery_release_eligibility(value: str) -> EnvironmentGitRecoveryReleaseEligibility:
    if value in ENVIRONMENT_GIT_RECOVERY_RELEASE_ELIGIBILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_RECOVERY_RELEASE_ELIGIBILITY_VALUES!r}"
    )
