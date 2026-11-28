from typing import Literal

GitHubDeploymentPolicyPreviewMode = Literal["app", "branch"]

GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_MODE_VALUES: set[GitHubDeploymentPolicyPreviewMode] = {
    "app",
    "branch",
}


def check_git_hub_deployment_policy_preview_mode(value: str) -> GitHubDeploymentPolicyPreviewMode:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PREVIEW_MODE_VALUES!r}")
