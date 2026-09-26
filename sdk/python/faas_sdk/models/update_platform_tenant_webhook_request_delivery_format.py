from typing import Literal

UpdatePlatformTenantWebhookRequestDeliveryFormat = Literal["cloudevents", "json"]

UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[UpdatePlatformTenantWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_update_platform_tenant_webhook_request_delivery_format(
    value: str,
) -> UpdatePlatformTenantWebhookRequestDeliveryFormat:
    if value in UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {UPDATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
