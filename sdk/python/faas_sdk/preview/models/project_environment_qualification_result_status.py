from typing import Literal

ProjectEnvironmentQualificationResultStatus = Literal["failed", "passed"]

PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_STATUS_VALUES: set[ProjectEnvironmentQualificationResultStatus] = {
    "failed",
    "passed",
}


def check_project_environment_qualification_result_status(value: str) -> ProjectEnvironmentQualificationResultStatus:
    if value in PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_STATUS_VALUES!r}"
    )
