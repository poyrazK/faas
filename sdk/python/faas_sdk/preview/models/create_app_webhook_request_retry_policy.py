from typing import Literal

CreateAppWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

CREATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[CreateAppWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_create_app_webhook_request_retry_policy(value: str) -> CreateAppWebhookRequestRetryPolicy:
    if value in CREATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}")
