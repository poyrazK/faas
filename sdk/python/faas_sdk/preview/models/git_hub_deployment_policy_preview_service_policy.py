from typing import Literal

GitHubDeploymentPolicyPreviewServicePolicy = Literal["allow_marked", "deny"]

GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_SERVICE_POLICY_VALUES: set[GitHubDeploymentPolicyPreviewServicePolicy] = {
    "allow_marked",
    "deny",
}


def check_git_hub_deployment_policy_preview_service_policy(value: str) -> GitHubDeploymentPolicyPreviewServicePolicy:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_SERVICE_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_SERVICE_POLICY_VALUES!r}"
    )
