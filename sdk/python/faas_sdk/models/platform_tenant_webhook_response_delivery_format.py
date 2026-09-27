from typing import Literal

PlatformTenantWebhookResponseDeliveryFormat = Literal["cloudevents", "json"]

PLATFORM_TENANT_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES: set[PlatformTenantWebhookResponseDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_platform_tenant_webhook_response_delivery_format(value: str) -> PlatformTenantWebhookResponseDeliveryFormat:
    if value in PLATFORM_TENANT_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PLATFORM_TENANT_WEBHOOK_RESPONSE_DELIVERY_FORMAT_VALUES!r}"
    )
