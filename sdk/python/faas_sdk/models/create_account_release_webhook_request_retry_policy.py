from typing import Literal

CreateAccountReleaseWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[CreateAccountReleaseWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_create_account_release_webhook_request_retry_policy(
    value: str,
) -> CreateAccountReleaseWebhookRequestRetryPolicy:
    if value in CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ACCOUNT_RELEASE_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}"
    )
