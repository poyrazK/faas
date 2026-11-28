from typing import Literal

ProjectEnvironmentQualificationResultErrorCode = Literal["preview_unavailable", "request_failed", "unexpected_status"]

PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_ERROR_CODE_VALUES: set[ProjectEnvironmentQualificationResultErrorCode] = {
    "preview_unavailable",
    "request_failed",
    "unexpected_status",
}


def check_project_environment_qualification_result_error_code(
    value: str,
) -> ProjectEnvironmentQualificationResultErrorCode:
    if value in PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_ERROR_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUALIFICATION_RESULT_ERROR_CODE_VALUES!r}"
    )
