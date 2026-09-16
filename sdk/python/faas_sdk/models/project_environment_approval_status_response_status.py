from typing import Literal

ProjectEnvironmentApprovalStatusResponseStatus = Literal["consumed", "expired", "pending"]

PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentApprovalStatusResponseStatus] = {
    "consumed",
    "expired",
    "pending",
}


def check_project_environment_approval_status_response_status(
    value: str,
) -> ProjectEnvironmentApprovalStatusResponseStatus:
    if value in PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_STATUS_VALUES!r}"
    )
