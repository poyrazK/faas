from typing import Literal

ProjectEnvironmentRoutePolicyResponseOwnership = Literal["application", "environment"]

PROJECT_ENVIRONMENT_ROUTE_POLICY_RESPONSE_OWNERSHIP_VALUES: set[ProjectEnvironmentRoutePolicyResponseOwnership] = {
    "application",
    "environment",
}


def check_project_environment_route_policy_response_ownership(
    value: str,
) -> ProjectEnvironmentRoutePolicyResponseOwnership:
    if value in PROJECT_ENVIRONMENT_ROUTE_POLICY_RESPONSE_OWNERSHIP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_ROUTE_POLICY_RESPONSE_OWNERSHIP_VALUES!r}"
    )
