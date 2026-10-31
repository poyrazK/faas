from typing import Literal

ProjectEnvironmentApprovalResponseTokenKind = Literal["plan", "promotion"]

PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_TOKEN_KIND_VALUES: set[ProjectEnvironmentApprovalResponseTokenKind] = {
    "plan",
    "promotion",
}


def check_project_environment_approval_response_token_kind(value: str) -> ProjectEnvironmentApprovalResponseTokenKind:
    if value in PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_TOKEN_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_APPROVAL_RESPONSE_TOKEN_KIND_VALUES!r}"
    )
