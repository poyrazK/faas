from typing import Literal

PlatformTenantWebhookResponseScope = Literal["platform_tenant"]

PLATFORM_TENANT_WEBHOOK_RESPONSE_SCOPE_VALUES: set[PlatformTenantWebhookResponseScope] = {
    "platform_tenant",
}


def check_platform_tenant_webhook_response_scope(value: str) -> PlatformTenantWebhookResponseScope:
    if value in PLATFORM_TENANT_WEBHOOK_RESPONSE_SCOPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_WEBHOOK_RESPONSE_SCOPE_VALUES!r}")
