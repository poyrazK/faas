from typing import Literal

ProjectEnvironmentCloneResourceResponseStatus = Literal[
    "captured",
    "capturing",
    "compensated",
    "compensating",
    "copying",
    "failed",
    "planned",
    "ready",
    "unsupported",
    "verifying",
]

PROJECT_ENVIRONMENT_CLONE_RESOURCE_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentCloneResourceResponseStatus] = {
    "captured",
    "capturing",
    "compensated",
    "compensating",
    "copying",
    "failed",
    "planned",
    "ready",
    "unsupported",
    "verifying",
}


def check_project_environment_clone_resource_response_status(
    value: str,
) -> ProjectEnvironmentCloneResourceResponseStatus:
    if value in PROJECT_ENVIRONMENT_CLONE_RESOURCE_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_CLONE_RESOURCE_RESPONSE_STATUS_VALUES!r}"
    )
