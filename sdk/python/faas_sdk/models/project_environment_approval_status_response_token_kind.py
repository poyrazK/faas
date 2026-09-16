from typing import Literal

ProjectEnvironmentApprovalStatusResponseTokenKind = Literal["plan", "promotion"]

PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_TOKEN_KIND_VALUES: set[
    ProjectEnvironmentApprovalStatusResponseTokenKind
] = {
    "plan",
    "promotion",
}


def check_project_environment_approval_status_response_token_kind(
    value: str,
) -> ProjectEnvironmentApprovalStatusResponseTokenKind:
    if value in PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_TOKEN_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_APPROVAL_STATUS_RESPONSE_TOKEN_KIND_VALUES!r}"
    )
