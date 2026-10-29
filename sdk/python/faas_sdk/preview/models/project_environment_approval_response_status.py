from typing import Literal

ProjectEnvironmentApprovalResponseStatus = Literal["consumed", "expired", "pending"]

PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_STATUS_VALUES: set[ProjectEnvironmentApprovalResponseStatus] = {
    "consumed",
    "expired",
    "pending",
}


def check_project_environment_approval_response_status(value: str) -> ProjectEnvironmentApprovalResponseStatus:
    if value in PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_STATUS_VALUES!r}"
    )
