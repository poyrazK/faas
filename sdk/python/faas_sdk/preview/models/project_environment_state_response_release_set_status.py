from typing import Literal

ProjectEnvironmentStateResponseReleaseSetStatus = Literal["active", "none"]

PROJECT_ENVIRONMENT_STATE_RESPONSE_RELEASE_SET_STATUS_VALUES: set[ProjectEnvironmentStateResponseReleaseSetStatus] = {
    "active",
    "none",
}


def check_project_environment_state_response_release_set_status(
    value: str,
) -> ProjectEnvironmentStateResponseReleaseSetStatus:
    if value in PROJECT_ENVIRONMENT_STATE_RESPONSE_RELEASE_SET_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_STATE_RESPONSE_RELEASE_SET_STATUS_VALUES!r}"
    )
