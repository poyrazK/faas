from typing import Literal

GitHubDeploymentPolicyPatchPreviewMode = Literal["app", "branch"]

GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_MODE_VALUES: set[GitHubDeploymentPolicyPatchPreviewMode] = {
    "app",
    "branch",
}


def check_git_hub_deployment_policy_patch_preview_mode(value: str) -> GitHubDeploymentPolicyPatchPreviewMode:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_MODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_MODE_VALUES!r}"
    )
