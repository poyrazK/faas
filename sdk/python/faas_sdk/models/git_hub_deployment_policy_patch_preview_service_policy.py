from typing import Literal

GitHubDeploymentPolicyPatchPreviewServicePolicy = Literal["allow_marked", "deny"]

GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_SERVICE_POLICY_VALUES: set[GitHubDeploymentPolicyPatchPreviewServicePolicy] = {
    "allow_marked",
    "deny",
}


def check_git_hub_deployment_policy_patch_preview_service_policy(
    value: str,
) -> GitHubDeploymentPolicyPatchPreviewServicePolicy:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_SERVICE_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PATCH_PREVIEW_SERVICE_POLICY_VALUES!r}"
    )
