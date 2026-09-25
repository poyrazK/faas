from typing import Literal

UpdatePlatformTenantWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[UpdatePlatformTenantWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_update_platform_tenant_webhook_request_retry_policy(
    value: str,
) -> UpdatePlatformTenantWebhookRequestRetryPolicy:
    if value in UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}"
    )
