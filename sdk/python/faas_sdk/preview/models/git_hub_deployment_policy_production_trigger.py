from typing import Literal

GitHubDeploymentPolicyProductionTrigger = Literal["actions", "webhook"]

GIT_HUB_DEPLOYMENT_POLICY_PRODUCTION_TRIGGER_VALUES: set[GitHubDeploymentPolicyProductionTrigger] = {
    "actions",
    "webhook",
}


def check_git_hub_deployment_policy_production_trigger(value: str) -> GitHubDeploymentPolicyProductionTrigger:
    if value in GIT_HUB_DEPLOYMENT_POLICY_PRODUCTION_TRIGGER_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {GIT_HUB_DEPLOYMENT_POLICY_PRODUCTION_TRIGGER_VALUES!r}"
    )
