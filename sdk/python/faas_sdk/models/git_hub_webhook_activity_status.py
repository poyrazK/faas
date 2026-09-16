from typing import Literal

GitHubWebhookActivityStatus = Literal["dead", "pending", "processing", "succeeded"]

GIT_HUB_WEBHOOK_ACTIVITY_STATUS_VALUES: set[GitHubWebhookActivityStatus] = {
    "dead",
    "pending",
    "processing",
    "succeeded",
}


def check_git_hub_webhook_activity_status(value: str) -> GitHubWebhookActivityStatus:
    if value in GIT_HUB_WEBHOOK_ACTIVITY_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {GIT_HUB_WEBHOOK_ACTIVITY_STATUS_VALUES!r}")
