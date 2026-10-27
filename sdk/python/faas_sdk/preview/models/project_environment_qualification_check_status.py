from typing import Literal

ProjectEnvironmentQualificationCheckStatus = Literal["failed", "passed"]

PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_STATUS_VALUES: set[ProjectEnvironmentQualificationCheckStatus] = {
    "failed",
    "passed",
}


def check_project_environment_qualification_check_status(value: str) -> ProjectEnvironmentQualificationCheckStatus:
    if value in PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_STATUS_VALUES!r}"
    )
