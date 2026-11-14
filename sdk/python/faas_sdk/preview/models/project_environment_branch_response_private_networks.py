from typing import Literal

ProjectEnvironmentBranchResponsePrivateNetworks = Literal["join", "off"]

PROJECT_ENVIRONMENT_BRANCH_RESPONSE_PRIVATE_NETWORKS_VALUES: set[ProjectEnvironmentBranchResponsePrivateNetworks] = {
    "join",
    "off",
}


def check_project_environment_branch_response_private_networks(
    value: str,
) -> ProjectEnvironmentBranchResponsePrivateNetworks:
    if value in PROJECT_ENVIRONMENT_BRANCH_RESPONSE_PRIVATE_NETWORKS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BRANCH_RESPONSE_PRIVATE_NETWORKS_VALUES!r}"
    )
