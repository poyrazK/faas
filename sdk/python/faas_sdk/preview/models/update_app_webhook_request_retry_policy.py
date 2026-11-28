from typing import Literal

UpdateAppWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

UPDATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[UpdateAppWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_update_app_webhook_request_retry_policy(value: str) -> UpdateAppWebhookRequestRetryPolicy:
    if value in UPDATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPDATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}")
