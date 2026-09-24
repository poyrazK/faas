from typing import Literal

UpdateAccountReleaseWebhookRequestEventFilterItem = Literal[
    "deployment.failed", "deployment.live", "rollout.aborted", "rollout.completed"
]

UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES: set[
    UpdateAccountReleaseWebhookRequestEventFilterItem
] = {
    "deployment.failed",
    "deployment.live",
    "rollout.aborted",
    "rollout.completed",
}


def check_update_account_release_webhook_request_event_filter_item(
    value: str,
) -> UpdateAccountReleaseWebhookRequestEventFilterItem:
    if value in UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES!r}"
    )
