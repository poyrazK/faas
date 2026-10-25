from typing import Literal

ProjectReleaseVerificationPercent = Literal[5, 25, 100]

PROJECT_RELEASE_VERIFICATION_PERCENT_VALUES: set[ProjectReleaseVerificationPercent] = {
    5,
    25,
    100,
}


def check_project_release_verification_percent(value: int) -> ProjectReleaseVerificationPercent:
    if value in PROJECT_RELEASE_VERIFICATION_PERCENT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROJECT_RELEASE_VERIFICATION_PERCENT_VALUES!r}")
