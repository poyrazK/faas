from typing import Literal

EnvironmentGitOpsChangedWebhookPayloadVersion = Literal[1]

ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES: set[EnvironmentGitOpsChangedWebhookPayloadVersion] = {
    1,
}


def check_environment_git_ops_changed_webhook_payload_version(
    value: int,
) -> EnvironmentGitOpsChangedWebhookPayloadVersion:
    if value in ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_VERSION_VALUES!r}"
    )
