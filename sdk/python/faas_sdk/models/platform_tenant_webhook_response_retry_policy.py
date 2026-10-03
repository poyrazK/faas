from typing import Literal

PlatformTenantWebhookResponseRetryPolicy = Literal["aggressive", "default", "none"]

PLATFORM_TENANT_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES: set[PlatformTenantWebhookResponseRetryPolicy] = {
    "aggressive",
    "default",
    "none",
}


def check_platform_tenant_webhook_response_retry_policy(value: str) -> PlatformTenantWebhookResponseRetryPolicy:
    if value in PLATFORM_TENANT_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_WEBHOOK_RESPONSE_RETRY_POLICY_VALUES!r}"
    )
