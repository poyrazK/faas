from typing import Literal

AccountReleaseWebhookResponseEventFilterItem = Literal[
    "deployment.failed", "deployment.live", "rollout.aborted", "rollout.completed"
]

ACCOUNT_RELEASE_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES: set[AccountReleaseWebhookResponseEventFilterItem] = {
    "deployment.failed",
    "deployment.live",
    "rollout.aborted",
    "rollout.completed",
}


def check_account_release_webhook_response_event_filter_item(
    value: str,
) -> AccountReleaseWebhookResponseEventFilterItem:
    if value in ACCOUNT_RELEASE_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_RELEASE_WEBHOOK_RESPONSE_EVENT_FILTER_ITEM_VALUES!r}"
    )
