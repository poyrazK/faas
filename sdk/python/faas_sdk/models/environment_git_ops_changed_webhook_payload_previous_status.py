from typing import Literal

EnvironmentGitOpsChangedWebhookPayloadPreviousStatus = Literal[
    "blocked", "drifted", "in_sync", "unavailable", "unknown", "verified"
]

ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES: set[
    EnvironmentGitOpsChangedWebhookPayloadPreviousStatus
] = {
    "blocked",
    "drifted",
    "in_sync",
    "unavailable",
    "unknown",
    "verified",
}


def check_environment_git_ops_changed_webhook_payload_previous_status(
    value: str,
) -> EnvironmentGitOpsChangedWebhookPayloadPreviousStatus:
    if value in ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_CHANGED_WEBHOOK_PAYLOAD_PREVIOUS_STATUS_VALUES!r}"
    )
