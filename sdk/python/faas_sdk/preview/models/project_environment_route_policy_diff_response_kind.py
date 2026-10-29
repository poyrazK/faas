from typing import Literal

ProjectEnvironmentRoutePolicyDiffResponseKind = Literal["changed", "unchanged"]

PROJECT_ENVIRONMENT_ROUTE_POLICY_DIFF_RESPONSE_KIND_VALUES: set[ProjectEnvironmentRoutePolicyDiffResponseKind] = {
    "changed",
    "unchanged",
}


def check_project_environment_route_policy_diff_response_kind(
    value: str,
) -> ProjectEnvironmentRoutePolicyDiffResponseKind:
    if value in PROJECT_ENVIRONMENT_ROUTE_POLICY_DIFF_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_ROUTE_POLICY_DIFF_RESPONSE_KIND_VALUES!r}"
    )
