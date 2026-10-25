from typing import Literal

AppWebhookResponseRetryPolicy = Literal["aggressive", "default", "none"]

APP_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES: set[AppWebhookResponseRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_app_webhook_response_retry_policy(value: str) -> AppWebhookResponseRetryPolicy:
    if value in APP_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES!r}")
