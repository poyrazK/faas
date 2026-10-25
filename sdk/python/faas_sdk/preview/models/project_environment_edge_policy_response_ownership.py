from typing import Literal

ProjectEnvironmentEdgePolicyResponseOwnership = Literal["application", "environment"]

PROJECT_ENVIRONMENT_EDGE_POLICY_RESPONSE_OWNERSHIP_VALUES: set[ProjectEnvironmentEdgePolicyResponseOwnership] = {
    "application",
    "environment",
}


def check_project_environment_edge_policy_response_ownership(
    value: str,
) -> ProjectEnvironmentEdgePolicyResponseOwnership:
    if value in PROJECT_ENVIRONMENT_EDGE_POLICY_RESPONSE_OWNERSHIP_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_EDGE_POLICY_RESPONSE_OWNERSHIP_VALUES!r}"
    )
