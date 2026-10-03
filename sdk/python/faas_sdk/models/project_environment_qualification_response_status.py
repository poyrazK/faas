from typing import Literal

ProjectEnvironmentQualificationResponseStatus = Literal["failed", "passed"]

PROJECT_ENVIRONMENT_QUALIFICATION_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentQualificationResponseStatus] = {
    "failed",
    "passed",
}


def check_project_environment_qualification_response_status(
    value: str,
) -> ProjectEnvironmentQualificationResponseStatus:
    if value in PROJECT_ENVIRONMENT_QUALIFICATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUALIFICATION_RESPONSE_STATUS_VALUES!r}"
    )
