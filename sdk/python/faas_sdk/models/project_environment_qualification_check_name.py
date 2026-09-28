from typing import Literal

ProjectEnvironmentQualificationCheckName = Literal["health", "smoke"]

PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_NAME_VALUES: set[ProjectEnvironmentQualificationCheckName] = {
    "health",
    "smoke",
}


def check_project_environment_qualification_check_name(value: str) -> ProjectEnvironmentQualificationCheckName:
    if value in PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_NAME_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_QUALIFICATION_CHECK_NAME_VALUES!r}"
    )
