from typing import Literal

PlatformTenantCustomerLifecycleWebhookPayloadCustomerStatus = Literal["active", "revoked"]

PLATFORM_TENANT_CUSTOMER_LIFECYCLE_WEBHOOK_PAYLOAD_CUSTOMER_STATUS_VALUES: set[
    PlatformTenantCustomerLifecycleWebhookPayloadCustomerStatus
] = {
    "active",
    "revoked",
}


def check_platform_tenant_customer_lifecycle_webhook_payload_customer_status(
    value: str,
) -> PlatformTenantCustomerLifecycleWebhookPayloadCustomerStatus:
    if value in PLATFORM_TENANT_CUSTOMER_LIFECYCLE_WEBHOOK_PAYLOAD_CUSTOMER_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_CUSTOMER_LIFECYCLE_WEBHOOK_PAYLOAD_CUSTOMER_STATUS_VALUES!r}"
    )
