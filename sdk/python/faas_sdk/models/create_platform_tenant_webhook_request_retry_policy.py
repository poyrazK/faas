from typing import Literal

CreatePlatformTenantWebhookRequestRetryPolicy = Literal["aggressive", "default", "none"]

CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES: set[CreatePlatformTenantWebhookRequestRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_create_platform_tenant_webhook_request_retry_policy(
    value: str,
) -> CreatePlatformTenantWebhookRequestRetryPolicy:
    if value in CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_RETRY_POLICY_VALUES!r}"
    )
