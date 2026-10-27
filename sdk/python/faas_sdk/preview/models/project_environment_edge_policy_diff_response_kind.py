from typing import Literal

ProjectEnvironmentEdgePolicyDiffResponseKind = Literal["changed", "unchanged"]

PROJECT_ENVIRONMENT_EDGE_POLICY_DIFF_RESPONSE_KIND_VALUES: set[ProjectEnvironmentEdgePolicyDiffResponseKind] = {
    "changed",
    "unchanged",
}


def check_project_environment_edge_policy_diff_response_kind(
    value: str,
) -> ProjectEnvironmentEdgePolicyDiffResponseKind:
    if value in PROJECT_ENVIRONMENT_EDGE_POLICY_DIFF_RESPONSE_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_EDGE_POLICY_DIFF_RESPONSE_KIND_VALUES!r}"
    )
