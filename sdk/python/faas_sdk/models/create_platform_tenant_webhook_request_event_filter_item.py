from typing import Literal

CreatePlatformTenantWebhookRequestEventFilterItem = Literal[
    "platform_tenant.customer.linked",
    "platform_tenant.customer.offboarded",
    "platform_tenant.hostname.verified",
    "platform_tenant.statement.finalized",
    "platform_tenant.surface.certificate.changed",
    "platform_tenant.surface.deployment.changed",
]

CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES: set[
    CreatePlatformTenantWebhookRequestEventFilterItem
] = {
    "platform_tenant.customer.linked",
    "platform_tenant.customer.offboarded",
    "platform_tenant.hostname.verified",
    "platform_tenant.statement.finalized",
    "platform_tenant.surface.certificate.changed",
    "platform_tenant.surface.deployment.changed",
}


def check_create_platform_tenant_webhook_request_event_filter_item(
    value: str,
) -> CreatePlatformTenantWebhookRequestEventFilterItem:
    if value in CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_PLATFORM_TENANT_WEBHOOK_REQUEST_EVENT_FILTER_ITEM_VALUES!r}"
    )
