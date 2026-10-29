from typing import Literal

GitHubDeploymentPolicyPatchProductionTrigger = Literal["actions", "webhook"]

GIT_HUB_DEPLOYMENT_POLICY_PATCH_PRODUCTION_TRIGGER_VALUES: set[GitHubDeploymentPolicyPatchProductionTrigger] = {
    "actions",
    "webhook",
}


def check_git_hub_deployment_policy_patch_production_trigger(
    value: str,
) -> GitHubDeploymentPolicyPatchProductionTrigger:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PATCH_PRODUCTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PATCH_PRODUCTION_TRIGGER_VALUES!r}"
    )
