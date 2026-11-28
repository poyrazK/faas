from typing import Literal

ProjectEnvironmentBranchSummaryPrivateNetworks = Literal["join", "off"]

PROJECT_ENVIRONMENT_BRANCH_SUMMARY_PRIVATE_NETWORKS_VALUES: set[ProjectEnvironmentBranchSummaryPrivateNetworks] = {
    "join",
    "off",
}


def check_project_environment_branch_summary_private_networks(
    value: str,
) -> ProjectEnvironmentBranchSummaryPrivateNetworks:
    if value in PROJECT_ENVIRONMENT_BRANCH_SUMMARY_PRIVATE_NETWORKS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PROJECT_ENVIRONMENT_BRANCH_SUMMARY_PRIVATE_NETWORKS_VALUES!r}"
    )
