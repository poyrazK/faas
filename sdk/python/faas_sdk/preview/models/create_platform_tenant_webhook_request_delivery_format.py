from typing import Literal

CreatePlatformTenantWebhookRequestDeliveryFormat = Literal["cloudevents", "json"]

CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES: set[CreatePlatformTenantWebhookRequestDeliveryFormat] = {
    "cloudevents",
    "json",
}


def check_create_platform_tenant_webhook_request_delivery_format(
    value: str,
) -> CreatePlatformTenantWebhookRequestDeliveryFormat:
    if value in CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_DELIVERY_FORMAT_VALUES!r}"
    )
