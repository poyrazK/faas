from typing import Literal

CreateAccountReleaseWebhookRequestEventFilterItem = Literal[
    "deployment.failed", "deployment.live", "rollout.aborted", "rollout.completed"
]

CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES: set[
    CreateAccountReleaseWebhookRequestEventFilterItem
] = {
    "deployment.failed",
    "deployment.live",
    "rollout.aborted",
    "rollout.completed",
}


def check_create_account_release_webhook_request_event_filter_item(
    value: str,
) -> CreateAccountReleaseWebhookRequestEventFilterItem:
    if value in CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES!r}"
    )
