from typing import Literal

AccountReleaseWebhookResponseRetryPolicy = Literal["aggressive", "default", "none"]

ACCOUNT_RELEASE_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES: set[AccountReleaseWebhookResponseRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_account_release_webhook_response_retry_policy(value: str) -> AccountReleaseWebhookResponseRetryPolicy:
    if value in ACCOUNT_RELEASE_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ACCOUNT_RELEASE_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES!r}"
    )
