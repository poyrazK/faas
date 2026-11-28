from typing import Literal

CreateProjectEnvironmentBranchRequestPrivateNetworks = Literal["join", "off"]

CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_PRIVATE_NETWORKS_VALUES: set[
    CreateProjectEnvironmentBranchRequestPrivateNetworks
] = {
    "join",
    "off",
}


def check_create_project_environment_branch_request_private_networks(
    value: str,
) -> CreateProjectEnvironmentBranchRequestPrivateNetworks:
    if value in CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_PRIVATE_NETWORKS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PROJECT_ENVIRONMENT_BRANCH_REQUEST_PRIVATE_NETWORKS_VALUES!r}"
    )
