from typing import Literal

EnvironmentGitOpsChangedWebhookPayloadStatus = Literal["blocked", "drifted", "in_sync", "unavailable", "verified"]

ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES: set[EnvironmentGitOpsChangedWebhookPayloadStatus] = {
    "blocked",
    "drifted",
    "in_sync",
    "unavailable",
    "verified",
}


def check_environment_git_ops_changed_webhook_payload_status(
    value: str,
) -> EnvironmentGitOpsChangedWebhookPayloadStatus:
    if value in ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_STATUS_VALUES!r}"
    )
