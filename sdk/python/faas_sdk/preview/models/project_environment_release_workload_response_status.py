from typing import Literal

ProjectEnvironmentReleaseWorkloadResponseStatus = Literal["live", "not_deployed"]

PROJECT_ENVIRONMENT_RELEASE_WORKLOAD_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentReleaseWorkloadResponseStatus] = {
    "live",
    "not_deployed",
}


def check_project_environment_release_workload_response_status(
    value: str,
) -> ProjectEnvironmentReleaseWorkloadResponseStatus:
    if value in PROJECT_ENVIRONMENT_RELEASE_WORKLOAD_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_RELEASE_WORKLOAD_RESPONSE_STATUS_VALUES!r}"
    )
