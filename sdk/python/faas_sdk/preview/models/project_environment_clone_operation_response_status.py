from typing import Literal

ProjectEnvironmentCloneOperationResponseStatus = Literal[
    "capturing", "compensated", "compensating", "copying", "failed", "pending", "publishing", "ready"
]

PROJECT_ENVIRONMENT_CLONE_OPERATION_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentCloneOperationResponseStatus] = {
    "capturing",
    "compensated",
    "compensating",
    "copying",
    "failed",
    "pending",
    "publishing",
    "ready",
}


def check_project_environment_clone_operation_response_status(
    value: str,
) -> ProjectEnvironmentCloneOperationResponseStatus:
    if value in PROJECT_ENVIRONMENT_CLONE_OPERATION_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_CLONE_OPERATION_RESPONSE_STATUS_VALUES!r}"
    )
