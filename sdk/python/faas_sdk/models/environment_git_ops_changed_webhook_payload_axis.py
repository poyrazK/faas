from typing import Literal

EnvironmentGitOpsChangedWebhookPayloadAxis = Literal["drift", "rollout", "source"]

ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_AXIS_VALUES: set[EnvironmentGitOpsChangedWebhookPayloadAxis] = {
    "drift",
    "rollout",
    "source",
}


def check_environment_git_ops_changed_webhook_payload_axis(value: str) -> EnvironmentGitOpsChangedWebhookPayloadAxis:
    if value in ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_AXIS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_AXIS_VALUES!r}"
    )
