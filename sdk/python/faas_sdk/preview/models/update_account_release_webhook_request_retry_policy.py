from typing import Literal

UpdateAccountReleaseWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[UpdateAccountReleaseWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_update_account_release_webhook_request_retry_policy(
    value: str,
) -> UpdateAccountReleaseWebhookRequestRetryPolicy:
    if value in UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}"
    )
